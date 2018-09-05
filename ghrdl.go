package main

import (
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

	"bitbucket.org/shu_go/gli"
	"bitbucket.org/shu_go/progio"
	"github.com/PuerkitoBio/goquery"
)

const (
	versionFile = "version"
)

type globalCmd struct {
	URL     string `help:"Github releases page"`
	Pattern string `help:"href pattern that contains '(?P<version>)'"`
	Dir     string `help:"download dest and version storage dir"      default:"."`
	version string
}

func (g *globalCmd) Before() {
	content, err := ioutil.ReadFile(filepath.Join(g.Dir, versionFile))
	if err == nil {
		g.version = strings.TrimSpace(string(content))
	}
}

func (g globalCmd) Run() error {
	dlurl, dlver, err := findFileURL(g.URL, g.Pattern)
	if err != nil {
		return fmt.Errorf("scrape ghr: %v", err)
	}

	// test you should download a file

	if !isNewer(g.version, dlver) {
		fmt.Println("no new release")
		return nil
	}

	dlurl = resolveURL(g.URL, dlurl)
	fmt.Printf("downloading %s ...\n", dlurl)

	// fetch the file

	resp, err := http.Get(dlurl)
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
	file, err := os.Create(filepath.Join(g.Dir, path.Base(dlurl)))
	if err != nil {
		return fmt.Errorf("create a file %v: %v", filepath.Join(g.Dir, path.Base(dlurl)), err)
	}
	defer file.Close()

	progreader := progio.NewReader(
		resp.Body,
		func(p int64) {
			fmt.Printf("%d%%  ", p)
		},
		progio.Percent(resp.ContentLength, 5),
	)
	_, err = io.Copy(file, progreader)
	if err != nil {
		return fmt.Errorf("copy content: %v", err)
	}

	err = ioutil.WriteFile(filepath.Join(g.Dir, versionFile), []byte(dlver), os.ModePerm)

	return err
}

func isNewer(curr, dl string) bool {
	return curr != dl
	/*
		sep := regexp.MustCompile("[^0-9a-zA-Z]")
		currcomp := sep.Split(curr)
		dlcomp := sep.Split(dl)
	*/
}

func resolveURL(base, relative string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return ""
	}

	if strings.HasPrefix(relative, "/") {
		return fmt.Sprintf("%s://%s%s", baseURL.Scheme, baseURL.Host, relative)
	}
	return path.Join(base, relative)
}

func findFileURL(url, pattern string) (fileURL, version string, err error) {
	ptn := regexp.MustCompile(pattern)

	// pattern check

	veridx := 0
	for i, n := range ptn.SubexpNames() {
		if n == "version" {
			veridx = i
			break
		}
	}
	if veridx == 0 {
		return "", "", errors.New("(?P<version>...) is required")
	}

	// scraping Github releases page

	doc, err := goquery.NewDocument(url)
	if err != nil {
		return "", "", fmt.Errorf("opening URL %v: %v", url, err)
	}

	// find first download link
	var dlurl, dlver string
	doc.Find("a").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		url, _ := s.Attr("href")
		if subs := ptn.FindStringSubmatch(url); subs != nil {
			dlurl = url
			dlver = subs[veridx]
			return false
		}
		return true
	})

	return dlurl, dlver, nil
}

func main() {
	app := gli.NewWith(&globalCmd{})
	app.Run(os.Args)
}
