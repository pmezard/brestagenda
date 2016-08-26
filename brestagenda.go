package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/alecthomas/kingpin"
)

type Event struct {
	Title    string
	Desc     string
	Category string
	Link     string
	Start    time.Time
	End      time.Time
}

func extractEvents(doc *goquery.Document, baseUrl string) ([]Event, error) {
	timeLayout := "2006-01-02"

	var err error
	events := []Event{}
	items := doc.Find("article[class~='listItem']")
	for i := range items.Nodes {
		item := items.Eq(i)
		title := item.Find("h3[class~='title']").First().Text()
		desc := item.Find("div[class~='chapeau']").First().Text()
		cat := item.Find("p[class='category']").First().Text()
		link, _ := item.Find("a[class='linkView']").First().Attr("href")
		link = baseUrl + link
		dates := item.Find("p[class='date'] time")
		start, _ := dates.Eq(0).Attr("datetime")
		end, _ := dates.Eq(1).Attr("datetime")
		ev := Event{
			Title:    strings.TrimSpace(title),
			Desc:     strings.TrimSpace(desc),
			Category: strings.TrimSpace(cat),
			Link:     link,
		}
		ev.Start, err = time.Parse(timeLayout, start)
		if err != nil {
			return nil, err
		}
		if end != "" {
			ev.End, err = time.Parse(timeLayout, end)
			if err != nil {
				return nil, err
			}
		}
		events = append(events, ev)
	}
	return events, nil
}

type Page struct {
	Events []Event
	Next   string
}

func getPage(client *http.Client, url, base string) (*Page, error) {
	rsp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != 200 {
		return nil, fmt.Errorf("GET got %d", rsp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(rsp.Body)
	if err != nil {
		return nil, err
	}
	events, err := extractEvents(doc, base)
	if err != nil {
		return nil, err
	}
	s := doc.Find("link[rel='next']").First()
	url, _ = s.Attr("href")
	page := Page{
		Events: events,
		Next:   url,
	}
	return &page, nil
}

const PageTemplate = `
<html>
<header>
	<meta charset="utf-8">
</header>
<body>
<table>
	{{range .Before}}
	<tr>
		<td><a href="{{.Link}}">link</a></td>
		<td>{{.Start}}</td>
		<td>{{.End}}</td>
		<td>{{.Delta}}</td>
		<td>{{.Title}}</td>
	</tr>
	{{end}}
	{{if .HasAfter}}
</table>
<hr/>
<table>
	{{end}}
	{{range .After}}
	<tr>
		<td><a href="{{.Link}}">link</a></td>
		<td>{{.Start}}</td>
		<td>{{.End}}</td>
		<td>{{.Delta}}</td>
		<td>{{.Title}}</td>
	</tr>
	{{end}}
</table>
</body>
</html>
`

func writeHtml(w io.Writer, events []Event) error {
	t, err := template.New("foo").Parse(PageTemplate)
	if err != nil {
		return err
	}

	type Entry struct {
		Link  string
		Start string
		End   string
		Delta string
		Title string
	}
	type Entries struct {
		Before   []Entry
		After    []Entry
		HasAfter bool
	}

	now := time.Now()
	entries := Entries{}
	for _, ev := range events {
		delta := int(ev.Start.Sub(now).Hours() / 24)
		deltaStr := ""
		if delta != 0 {
			deltaStr = fmt.Sprintf("%+dj", delta)
		}
		entry := Entry{
			Link:  ev.Link,
			Start: ev.Start.Format("2006-01-02"),
			Delta: deltaStr,
			Title: ev.Title,
		}
		if !ev.End.IsZero() {
			entry.End = ev.End.Format("2006-01-02")
		}
		if ev.Start.After(now) {
			entries.After = append(entries.After, entry)
		} else {
			entries.Before = append(entries.Before, entry)
		}
	}
	entries.HasAfter = len(entries.Before) > 0 && len(entries.After) > 0
	return t.Execute(w, &entries)
}

type sortedEvents []Event

func (ev sortedEvents) Len() int {
	return len(ev)
}
func (ev sortedEvents) Swap(i, j int) {
	ev[i], ev[j] = ev[j], ev[i]
}
func (ev sortedEvents) Less(i, j int) bool {
	return ev[i].Start.Before(ev[j].Start)
}

var (
	crawlCmd     = app.Command("crawl", "crawl brest.fr agenda")
	crawlPathArg = crawlCmd.Arg("path", "output HTML path").Required().String()
)

func crawlFn() error {
	outPath := *crawlPathArg

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	base := "https://www.brest.fr"
	path := "/actus-et-agenda/agenda-132.html"
	events := []Event{}
	for {
		url := base + path
		fmt.Println("GET", url)
		p, err := getPage(client, url, base)
		if err != nil {
			return err
		}
		path = p.Next
		events = append(events, p.Events...)
		if path == "" {
			break
		}
		fmt.Println(base + path)
	}
	fp, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer fp.Close()
	sort.Sort(sortedEvents(events))
	return writeHtml(fp, events)
}

var (
	app = kingpin.New("brestagenda", "Crawl and reformat brest.fr agenda")
)

func dispatch() error {
	cmd := kingpin.MustParse(app.Parse(os.Args[1:]))
	switch cmd {
	case crawlCmd.FullCommand():
		return crawlFn()
	}
	return fmt.Errorf("unknown command: %s", cmd)
}

func main() {
	err := dispatch()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}
