package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/alecthomas/kong"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gonejack/get"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	log.SetOutput(os.Stdout)
}
func main() {
	var c saveurl
	if e := c.run(); e != nil {
		log.Fatal(e)
	}
}

type saveurl struct {
	Verbose bool     `short:"v" help:"Verbose printing."`
	List    string   `short:"i" help:"URL list file."`
	About   bool     `help:"About."`
	URL     []string `arg:"" optional:""`
}

func (c *saveurl) run() error {
	kong.Parse(c,
		kong.Name("saveurls"),
		kong.Description("This command line tool fetches url as html"),
		kong.UsageOnError(),
	)

	if c.About {
		fmt.Println("Visit https://github.com/gonejack/saveurls")
		return nil
	}

	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			c.URL = append(c.URL, scan.Text())
		}
	}

	if c.List != "" {
		f, err := os.Open(c.List)
		if err != nil {
			return err
		}
		scan := bufio.NewScanner(f)
		for scan.Scan() {
			c.URL = append(c.URL, strings.TrimSpace(scan.Text()))
		}
		_ = f.Close()
	}

	if len(c.URL) == 0 {
		return fmt.Errorf("no urls given")
	}

	var semap = semaphore.NewWeighted(3)
	var group errgroup.Group

	for i := range c.URL {
		u := c.URL[i]
		semap.Acquire(context.TODO(), 1)
		group.Go(func() error {
			defer semap.Release(1)
			err := c.runOne(u)
			if err != nil {
				log.Printf("process %s failed: %s", u, err)
			}
			return err
		})
	}

	_ = group.Wait()

	return nil
}
func (c *saveurl) runOne(u string) error {
	if c.Verbose {
		log.Printf("processing %s", u)
	}

	uri, err := url.Parse(u)
	if err != nil {
		return err
	}

	tmpf, err := os.CreateTemp("", "temp")
	if err != nil {
		return err
	}
	_ = tmpf.Close()

	tmp := tmpf.Name()
	defer os.Remove(tmp)

	ref := uri.String()
	if c.Verbose {
		log.Printf("fetch %s", ref)
	}

	err = get.Download(get.NewDownloadTask(ref, tmp), time.Minute)
	if err != nil {
		return fmt.Errorf("download %s fail: %s", ref, err)
	}

	mime, err := mimetype.DetectFile(tmp)
	if err != nil {
		return fmt.Errorf("cannnot detect mime of %s: %s", tmp, err)
	}

	if mime.Extension() != ".html" {
		err = os.Rename(tmp, filepath.Join(".", filepath.Base(tmp)+mime.Extension()))
		if err != nil {
			err = fmt.Errorf("cannot move file: %s", err)
		}
		return err
	}

	htm, err := c.moveHTML(tmp)
	if err != nil {
		return fmt.Errorf("rename %s failed: %s", tmp, err)
	}

	err = c.patchHTML(ref, htm)
	if err != nil {
		return fmt.Errorf("patch %s fail: %s", htm, err)
	}

	return nil
}
func (c *saveurl) moveHTML(tmp string) (rename string, err error) {
	f, err := os.Open(tmp)
	if err != nil {
		return
	}
	defer f.Close()

	doc, err := goquery.NewDocumentFromReader(f)
	if err != nil {
		return "", fmt.Errorf("parse %s fail: %s", tmp, err)
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
		f, err := os.OpenFile(rename, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
		switch {
		case errors.Is(err, os.ErrExist):
			index += 1
			continue
		case err == nil:
			_ = f.Close()
			return rename, os.Rename(tmp, rename)
		default:
			return "", fmt.Errorf("create file %s fail: %s", rename, err)
		}
	}
}
func (c *saveurl) patchHTML(src, html string) (err error) {
	f, err := os.Open(html)
	if err != nil {
		return
	}
	defer f.Close()

	doc, err := goquery.NewDocumentFromReader(f)
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
			e.SetAttr(attr, c.patchRef(src, ref))
		}
	})
	doc.Find("body").AppendHtml(c.footer(src))

	htm, err := doc.Html()
	if err != nil {
		return
	}

	err = os.WriteFile(html, []byte(htm), 0666)
	if err != nil {
		return
	}

	return
}
func (c *saveurl) patchRef(pageRef, imgRef string) string {
	i, err := url.Parse(imgRef)
	if err != nil {
		return imgRef
	}
	p, err := url.Parse(pageRef)
	if err != nil {
		return imgRef
	}
	if i.Host == "" {
		i.Host = p.Host
	}
	if i.Scheme == "" {
		i.Scheme = p.Scheme
	}
	return i.String()
}
func (c *saveurl) footer(link string) string {
	const tpl = `
<br/><br/>
<div style="margin-left: 4px;">
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
