package saveurls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

var client = http.Client{}

type SaveURL struct {
	Options
}

func (c *SaveURL) Run() error {
	if len(c.URL) == 0 {
		return fmt.Errorf("no urls given")
	}
	return c.run()
}
func (c *SaveURL) run() error {
	var sema = semaphore.NewWeighted(3)
	var grp errgroup.Group
	for i := range c.URL {
		u := c.URL[i]

		sema.Acquire(context.TODO(), 1)
		grp.Go(func() (err error) {
			defer sema.Release(1)
			err = c.save(u)
			if err != nil {
				err = fmt.Errorf("process %s failed: %s", u, err)
			}
			return
		})
	}
	return grp.Wait()
}
func (c *SaveURL) save(ref string) (err error) {
	if c.Verbose {
		log.Printf("processing %s", ref)
	}

	timeout, cancel := context.WithTimeout(context.TODO(), time.Minute*2)
	defer cancel()

	req, err := http.NewRequestWithContext(timeout, http.MethodGet, ref, nil)
	if err != nil {
		return fmt.Errorf("nod valid url: %s", err)
	}
	req.Header.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:95.0) Gecko/20100101 Firefox/95.0")

	rsp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %s", err)
	}
	defer rsp.Body.Close()

	ct, _, _ := mime.ParseMediaType(rsp.Header.Get("content-type"))
	if ct != "text/html" {
		return fmt.Errorf("%s is not HTML page", ref)
	}

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return fmt.Errorf("download failed: %s", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("parse %s fail: %s", body, err)
	}
	c.patch(ref, doc)

	htm, err := doc.Html()
	if err != nil {
		return fmt.Errorf("cannot genreate html: %s", err)
	}

	title := doc.Find("title").Text()
	if title == "" {
		title = safeName(ref)
	} else {
		title = safeName(title)
	}

	name := fmt.Sprintf("%s.html", title)
	for idx := 1; idx <= 1e4; idx += 1 {
		fd, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0766)
		switch {
		case errors.Is(err, os.ErrExist):
			name = fmt.Sprintf("%s.%d.html", title, idx)
		case err == nil:
			_, err = fd.WriteString(htm)
			fd.Close()
			return err
		default:
			return err
		}
	}
	return
}
func (c *SaveURL) patch(url string, doc *goquery.Document) {
	doc.Find("img,video,source,link").Each(func(i int, e *goquery.Selection) {
		var attr string
		switch e.Get(0).Data {
		case "link":
			attr = "href"
		default:
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
			e.SetAttr(attr, c.patchURL(url, ref))
		}
	})
	doc.Find("body").AppendHtml(c.footer(url))
}
func (c *SaveURL) patchURL(pageURL, srcURL string) string {
	i, err := url.Parse(srcURL)
	if err != nil {
		return srcURL
	}
	p, err := url.Parse(pageURL)
	if err != nil {
		return srcURL
	}
	if i.Host == "" {
		i.Host = p.Host
	}
	if i.Scheme == "" {
		i.Scheme = p.Scheme
	}
	return i.String()
}
func (c *SaveURL) footer(link string) string {
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

func safeName(name string) string {
	return regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`).ReplaceAllString(name, ".")
}
