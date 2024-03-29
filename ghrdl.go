package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
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

func init() {
	if Version == "" {
		Version = "dev-" + time.Now().Format("20060102")
	}
}

const (
	versionFile = "version"
)

type globalCmd struct {
	URL     string `help:"a URL of GitHub Releases page"`
	Pattern string `cli:"pattern=[REGEXP|tarball|zipball]" help:"download URL pattern filter"`
	Dir     string `help:"download dest and version storage dir (default: ./{repos}"`
	Title   string `help:"notification title (default: --dir)"`
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
	content, err := ioutil.ReadFile(filepath.Join(g.Dir, versionFile))
	if err == nil {
		version = strings.TrimSpace(string(content))
	}

	lurl := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repos)
	resp, err := http.Get(lurl)
	if err != nil {
		return err
	}

	// read body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	var tagName string
	err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "/tag_name", &tagName)
	if err != nil {
		return err
	}

	// test you should download a file

	if !isNewer(version, tagName) {
		fmt.Printf("no new release (%v)\n", tagName)
		return nil
	}
	println(tagName)

	// determine the path to download

	var timestampStr string
	var assets []map[string]interface{}
	err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "assets", &assets)
	if err != nil {
		return err
	}

	var dlurl string
	if g.Pattern == "tarball" {
		err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "tarball_url", &dlurl)
		if err != nil {
			return err
		}
		_ = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "pushed_at", &timestampStr)
	} else if g.Pattern == "zipball" {
		err = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "zipball_url", &dlurl)
		if err != nil {
			return err
		}
		_ = scan.ScanJSON(bytes.NewBuffer(bodyBytes), "pushed_at", &timestampStr)
	} else {
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
				tmp, found := a["updated_at"]
				if found {
					timestampStr = tmp.(string)
				}
				break
			}
		}
	}

	if dlurl == "" {
		println("no match")
	}

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
	if g.Pattern == "tarball" {
		filename += ".tar.gz"
	} else if g.Pattern == "zipball" {
		filename += ".zip"
	}
	file, err := os.Create(filepath.Join(g.Dir, filename))
	if err != nil {
		return fmt.Errorf("create a file %v: %v", filepath.Join(g.Dir, path.Base(dlurl)), err)
	}
	defer file.Close()

	bar := progressbar.New(100)

	progreader := progio.NewReader(
		resp.Body,
		func(p int64) {
			bar.Add(1)
		},
		progio.Percent(resp.ContentLength, 1),
	)

	_, err = io.Copy(file, progreader)
	if err != nil {
		return fmt.Errorf("copy content: %v", err)
	}

	err = ioutil.WriteFile(filepath.Join(g.Dir, versionFile), []byte(tagName), os.ModePerm)
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

	err = beeep.Notify(g.Title+"(ghrdl)", tagName+" Downloaded", "" /*"assets/information.png"*/)
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
