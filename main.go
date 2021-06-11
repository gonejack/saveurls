package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"html"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gonejack/get"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

var (
	verbose = false
	list    string

	cmd = &cobra.Command{
		Use:   "saveurls urls...",
		Short: "Command line tool for fetching url as html",
		Run: func(c *cobra.Command, args []string) {
			err := run(c, args)
			if err != nil {
				log.Fatal(err)
			}
		},
	}
)

func init() {
	log.SetOutput(os.Stdout)

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "verbose")
	cmd.Flags().StringVarP(&list, "list", "i", "", "url list file")
}
func run(c *cobra.Command, urls []string) error {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			urls = append(urls, scan.Text())
		}
	}

	if list != "" {
		fd, err := os.Open(list)
		if err != nil {
			return err
		}
		scan := bufio.NewScanner(fd)
		for scan.Scan() {
			urls = append(urls, strings.TrimSpace(scan.Text()))
		}
		_ = fd.Close()
	}

	if len(urls) == 0 {
		return fmt.Errorf("no urls given")
	}

	var batch = semaphore.NewWeighted(3)
	var group errgroup.Group

	for _, u := range urls {
		if verbose {
			log.Printf("processing %s", u)
		}

		_ = batch.Acquire(context.TODO(), 1)
		group.Go(func() (err error) {
			defer batch.Release(1)

			uri, err := url.Parse(u)
			if err != nil {
				return
			}

			tmpfd, err := os.CreateTemp("", "temp")
			if err != nil {
				return
			}
			tmpfile := tmpfd.Name()

			defer func() {
				_ = tmpfd.Close()
				_ = os.Remove(tmpfile)
			}()

			ref := uri.String()
			if verbose {
				log.Printf("fetch %s", ref)
			}

			err = get.Download(ref, tmpfile, time.Minute)
			if err != nil {
				log.Printf("download %s fail: %s", ref, err)
				return
			}

			mime, err := mimetype.DetectFile(tmpfile)
			if err != nil {
				log.Printf("cannnot detect mime of %s: %s", tmpfile, err)
				return
			}
			if mime.Extension() != ".html" {
				saveAs := filepath.Join(".", filepath.Base(tmpfile)+mime.Extension())
				err = os.Rename(tmpfile, saveAs)
				if err != nil {
					log.Printf("cannot move file: %s", err)
				}
				return
			}

			htm, err := moveHTML(tmpfile)
			if err != nil {
				log.Printf("rename %s failed: %s", tmpfile, err)
				return
			}

			err = patchHTML(ref, htm)
			if err != nil {
				log.Printf("patch %s fail: %s", htm, err)
				return
			}

			return nil
		})

		_ = group.Wait()
	}

	return nil
}
func moveHTML(tmp string) (rename string, err error) {
	fd, err := os.Open(tmp)
	if err != nil {
		return
	}
	defer fd.Close()

	doc, err := goquery.NewDocumentFromReader(fd)
	if err != nil {
		log.Printf("parse %s fail: %s", tmp, err)
		return
	}

	title := doc.Find("title").Text()
	if title != "" {
		title = strings.ReplaceAll(title, "/", "_")
	}

	index := 0
	for {
		if index > 0 {
			rename = fmt.Sprintf("%s.%d.html", title, index)
		} else {
			rename = fmt.Sprintf("%s.html", title)
		}

		file, err := os.OpenFile(rename, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
		switch {
		case errors.Is(err, os.ErrExist):
			index += 1
			continue
		case err == nil:
			_ = file.Close()
			return rename, os.Rename(tmp, rename)
		default:
			return "", fmt.Errorf("create file %s fail: %s", rename, err)
		}
	}
}
func patchHTML(src, path string) (err error) {
	fd, err := os.Open(path)
	if err != nil {
		return
	}
	defer fd.Close()

	doc, err := goquery.NewDocumentFromReader(fd)
	if err != nil {
		return
	}

	doc.Find("img, link").Each(func(i int, e *goquery.Selection) {
		var attr string
		switch e.Get(0).Data {
		case "link":
			attr = "href"
		case "img":
			attr = "src"
			e.RemoveAttr("loading")
			e.RemoveAttr("srcset")
		}

		ref, _ := e.Attr(attr)
		switch {
		case ref == "":
			return
		case strings.HasPrefix(ref, "data:"):
			return
		case strings.HasPrefix(ref, "http://"):
			return
		case strings.HasPrefix(ref, "https://"):
			return
		default:
			e.SetAttr(attr, patchRef(src, ref))
		}
	})
	doc.Find("body").AppendHtml(footer(src))

	htm, err := doc.Html()
	if err != nil {
		return
	}

	err = ioutil.WriteFile(path, []byte(htm), 0666)
	if err != nil {
		return
	}

	return
}
func patchRef(pageRef, imgRef string) string {
	refURL, err := url.Parse(imgRef)
	if err != nil {
		return imgRef
	}

	pageURL, err := url.Parse(pageRef)
	if err != nil {
		return imgRef
	}

	if refURL.Host == "" {
		refURL.Host = pageURL.Host
	}
	if refURL.Scheme == "" {
		refURL.Scheme = pageURL.Scheme
	}

	return refURL.String()
}
func footer(link string) string {
	const tpl = `
<br/><br/>
<div style="margin-left: 2%;">
<a style="display: inline-block; border-top: 1px solid #ccc; padding-top: 5px; color: #666; text-decoration: none;"
   href="{link}">{linkText}</a>
<p style="color:#999;">Save with <a style="color:#666; text-decoration:none; font-weight: bold;" 
									href="https://github.com/gonejack/saveurls">saveurls</a>
</p>
</div>`

	linkText, err := url.QueryUnescape(link)
	if err != nil {
		linkText = link
	}

	return strings.NewReplacer(
		"{link}", link,
		"{linkText}", html.EscapeString(linkText),
	).Replace(tpl)
}
func main() {
	_ = cmd.Execute()
}
