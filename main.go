package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dustin/go-humanize"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var (
	client  http.Client
	verbose = false
	prog    = &cobra.Command{
		Use:   "saveurls *.txt",
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

	prog.Flags().SortFlags = false
	prog.PersistentFlags().SortFlags = false
	prog.PersistentFlags().BoolVarP(
		&verbose,
		"verbose",
		"v",
		false,
		"verbose",
	)
}

func run(c *cobra.Command, files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("no url list given")
	}

	for _, fp := range files {
		if verbose {
			log.Printf("processing %s", fp)
		}

		fd, err := os.Open(fp)
		if err != nil {
			return err
		}

		var batch = semaphore.NewWeighted(3)
		var group errgroup.Group

		scanner := bufio.NewScanner(fd)
		for scanner.Scan() {
			_ = batch.Acquire(context.TODO(), 1)

			text := scanner.Text()
			u, err := url.Parse(text)
			if err != nil {
				return err
			}

			temp, err := os.CreateTemp(".", "temp")
			if err != nil {
				return err
			}
			_ = temp.Close()

			src := u.String()
			target := temp.Name()
			group.Go(func() (err error) {
				defer batch.Release(1)

				if verbose {
					log.Printf("fetch %s", src)
				}

				err = download(src, target)
				if err != nil {
					log.Printf("download %s fail: %s", src, err)
					return
				}

				fd, err := os.Open(target)
				if err != nil {
					log.Printf("cannot open %s fail: %s", target, err)
					return
				}
				defer fd.Close()

				doc, err := goquery.NewDocumentFromReader(fd)
				if err != nil {
					log.Printf("parse %s fail: %s", target, err)
					return
				}

				title := doc.Find("title").Text()
				if title != "" {
					title = strings.ReplaceAll(title, "/", "_")
				}

				rename := fmt.Sprintf("%s.html", title)
				index := 1
				for {
					file, err := os.OpenFile(rename, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
					if err == nil {
						_ = file.Close()
						break
					}
					if errors.Is(err, os.ErrExist) {
						rename = fmt.Sprintf("%s[%d].html", title, index)
						index++
						continue
					} else {
						log.Printf("create file %s fail: %s", rename, err)
						return err
					}
				}
				err = os.Rename(target, rename)
				if err != nil {
					log.Printf("rename %s => %s fail: %s", target, rename, err)
					return
				}

				return nil
			})
		}

		_ = group.Wait()
	}

	return nil
}

func download(src, path string) (err error) {
	timeout, cancel := context.WithTimeout(context.TODO(), time.Minute*2)
	defer cancel()

	info, err := os.Stat(path)
	if err == nil && info.Size() > 0 {
		headReq, headErr := http.NewRequestWithContext(timeout, http.MethodHead, src, nil)
		if headErr != nil {
			return headErr
		}
		resp, headErr := client.Do(headReq)
		if headErr == nil && info.Size() == resp.ContentLength {
			return // skip download
		}
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return
	}
	defer file.Close()

	request, err := http.NewRequestWithContext(timeout, http.MethodGet, src, nil)
	if err != nil {
		return
	}
	response, err := client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()

	var written int64
	if verbose {
		bar := progressbar.NewOptions64(response.ContentLength,
			progressbar.OptionSetTheme(progressbar.Theme{Saucer: "=", SaucerPadding: ".", BarStart: "|", BarEnd: "|"}),
			progressbar.OptionSetWidth(10),
			progressbar.OptionSpinnerType(11),
			progressbar.OptionShowBytes(true),
			progressbar.OptionShowCount(),
			progressbar.OptionSetPredictTime(false),
			progressbar.OptionSetDescription(filepath.Base(src)),
			progressbar.OptionSetRenderBlankState(true),
			progressbar.OptionClearOnFinish(),
		)
		defer bar.Clear()
		written, err = io.Copy(io.MultiWriter(file, bar), response.Body)
	} else {
		written, err = io.Copy(file, response.Body)
	}

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("response status code %d invalid", response.StatusCode)
	}

	if err == nil && written < response.ContentLength {
		err = fmt.Errorf("expected %s but downloaded %s", humanize.Bytes(uint64(response.ContentLength)), humanize.Bytes(uint64(written)))
	}

	return
}

func main() {
	_ = prog.Execute()
}
