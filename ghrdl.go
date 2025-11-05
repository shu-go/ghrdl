package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gen2brain/beeep"
	scan "github.com/mattn/go-scan"
	"github.com/schollz/progressbar"
	"github.com/shu-go/gli"
	"github.com/shu-go/progio"
)

// Version is app version
var Version string

const (
	versionFile = "version"
)

/*
* Latest   : .../releases/latest
* Tag      : .../releases/tags/{Tag}
*
* 1. 上記のオプションに基づき、所定のエンドポイントにアクセスする。
* 2. Pattern の browser_download_url を持つモノを特定する。
* 3. バージョン比較を行う
* 	Latest -> tag_name の大小比較
* 	Tag -> assets/digest の文字列比較（違えば最新とみなす）
* */
type globalCmd struct {
	URL    string `help:"a URL of GitHub Releases page"`
	Latest bool   `default:"true" `
	Tag    string `help:"exclusive with --latest"`

	Pattern string `cli:"pattern=[REGEXP|tarball|zipball]" help:"download URL pattern filter"`
	Dir     string `help:"download dest and version storage dir (default: ./{repos}"`
	Title   string `help:"notification title (default: --dir)"`

	Debug bool
}

func (g *globalCmd) Before() error {
	if g.Debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	if g.Tag != "" {
		g.Latest = false
	} else if !g.Latest {
		return errors.New("--latest, without --tag is set")
	}

	slog.Debug(
		"Options",
		slog.String("URL", g.URL),
		slog.Bool("Latest", g.Latest),
		slog.String("Tag", g.Tag),
		slog.String("Pattern", g.Pattern),
	)

	return nil
}

func (g globalCmd) Run() error {
	u, err := url.Parse(g.URL)
	if err != nil {
		return err
	}

	if !strings.HasPrefix(u.Scheme, "http") {
		return errors.New("invalid url")
	}

	if g.Pattern == "" {
		return errors.New("invalid pattern")
	}

	pp := strings.Split(u.Path, "/")
	if len(pp) < 3 {
		return errors.New("invalid url")
	}

	owner := pp[1]
	repos := pp[2]

	if g.Dir == "" {
		g.Dir = filepath.Join(".", repos)
	}
	if g.Title == "" {
		g.Title = g.Dir
	}

	var version string
	content, err := os.ReadFile(filepath.Join(g.Dir, versionFile))
	if err == nil {
		version = strings.TrimSpace(string(content))
		slog.Debug(
			"read versionFile",
			slog.String("versionFile", versionFile),
			slog.String("version", version),
		)
	}

	var apiurl string
	if g.Latest {
		apiurl, err = url.JoinPath("https://api.github.com/repos", owner, repos, "releases/latest")
	} else {
		apiurl, err = url.JoinPath("https://api.github.com/repos", owner, repos, "releases/tags", g.Tag)
	}
	if err != nil {
		return err
	}
	slog.Debug(
		"api url",
		slog.Bool("Latest", g.Latest),
		slog.String("Tag", g.Tag),
		slog.String("apiurl", apiurl),
	)

	resp, err := http.Get(apiurl)
	if err != nil {
		return err
	}

	// read body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	// determine the path to download

	var timestampStr string
	var assets []map[string]any
	err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "assets", &assets)
	if err != nil {
		return err
	}

	var dlurl string
	var digest string
	switch g.Pattern {
	case "tarball":
		err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "tarball_url", &dlurl)
		if err != nil {
			return err
		}
		_ = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "pushed_at", &timestampStr)
	case "zipball":
		err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "zipball_url", &dlurl)
		if err != nil {
			return err
		}
		_ = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "pushed_at", &timestampStr)
	default:
		ptn := regexp.MustCompile(g.Pattern)
		for _, a := range assets {
			dlurli, found := a["browser_download_url"]
			if !found {
				continue
			}

			dlurl = dlurli.(string)
			if ptn.FindString(dlurl) == "" {
				dlurl = ""
			} else {
				if tmp, found := a["digest"]; found {
					if d, ok := tmp.(string); ok {
						digest = d
					}
				}
				if tmp, found := a["updated_at"]; found {
					timestampStr = tmp.(string)
				}
				break
			}
		}
	}

	if dlurl == "" {
		println("no match")
	}

	// check if updated

	var newversion string
	if g.Latest {
		err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "/tag_name", &newversion)
		if err != nil {
			return err
		}

		// test you should download a file

		if !isNewer(version, newversion) {
			fmt.Printf("no new release (%v)\n", newversion)
			return nil
		}
	} else { // specific g.Tag
		newversion = digest
		if newversion == version {
			fmt.Printf("no new release (%v)\n", newversion)
			return nil
		}
	}
	println(newversion)

	// fetch the file

	resp, err = http.Get(dlurl)
	if err != nil {
		return fmt.Errorf("download %v: %v", dlurl, err)
	}
	defer resp.Body.Close()

	// mkdir
	err = os.MkdirAll(g.Dir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("create directories: %v", err)
	}

	// store
	filename := path.Base(dlurl)
	switch g.Pattern {
	case "tarball":
		filename += ".tar.gz"
	case "zipball":
		filename += ".zip"
	}
	file, err := os.Create(filepath.Join(g.Dir, filename))
	if err != nil {
		return fmt.Errorf("create a file %v: %v", filepath.Join(g.Dir, path.Base(dlurl)), err)
	}
	defer file.Close()

	bar := progressbar.New(100)

	listener := func(p int64) {
		bar.Add(1)
	}
	progreader := progio.NewReader(
		resp.Body,
		listener,
		progio.Percent(resp.ContentLength, 1),
	)

	_, err = io.Copy(file, progreader)
	if err != nil {
		return fmt.Errorf("copy content: %v", err)
	}

	err = os.WriteFile(filepath.Join(g.Dir, versionFile), []byte(newversion), os.ModePerm)
	if err != nil {
		return err
	}

	timestamp := time.Now()
	if timestampStr != "" {
		ts, err := time.Parse(time.RFC3339, timestampStr)
		if err == nil {
			timestamp = ts
		}
	}
	_ = os.Chtimes(filepath.Join(g.Dir, filename), timestamp, timestamp)

	err = beeep.Notify(g.Title+"(ghrdl)", newversion+" Downloaded", "" /*"assets/information.png"*/)
	if err != nil {
		return err
	}

	return nil
}

func isNewer(curr, dl string) bool {
	return curr != dl
}

func main() {
	app := gli.NewWith(&globalCmd{})
	app.Name = "ghrdl"
	app.Desc = "Download GitHub Releases"
	app.Version = Version
	app.Usage = ``
	app.Copyright = "(C) 2021 Shuhei Kubota"
	app.Run(os.Args)

}
