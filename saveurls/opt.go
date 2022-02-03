package saveurls

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

type about bool

func (a about) BeforeApply() (err error) {
	fmt.Println("Visit https://github.com/gonejack/saveurls")
	os.Exit(0)
	return
}

type Options struct {
	List     string   `short:"i" help:"URL list file."`
	NoFooter bool     `help:"Do not append footer"`
	Verbose  bool     `short:"v" help:"Verbose printing."`
	About    about    `help:"About."`
	URL      []string `arg:"" optional:""`
}

func MustParseOptions() (opt Options) {
	kong.Parse(&opt,
		kong.Name("saveurls"),
		kong.Description("This command line tool saves url as .html file"),
		kong.UsageOnError(),
	)
	if s, _ := os.Stdin.Stat(); (s.Mode() & os.ModeCharDevice) == 0 {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			opt.URL = append(opt.URL, strings.TrimSpace(sc.Text()))
		}
	}
	if opt.List != "" {
		f, err := os.Open(opt.List)
		if err != nil {
			return
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			opt.URL = append(opt.URL, strings.TrimSpace(sc.Text()))
		}
		_ = f.Close()
	}
	for i, u := range opt.URL {
		if !strings.HasPrefix(u, "http") {
			patched := "http://" + u
			if opt.Verbose {
				log.Printf("patch %s as %s", u, patched)
			}
			opt.URL[i] = patched
		}
	}
	return
}
