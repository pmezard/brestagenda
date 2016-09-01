package main

import (
	"encoding/json"
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

var (
	crawlCmd     = app.Command("crawl", "crawl brest.fr agenda")
	crawlPathArg = crawlCmd.Arg("path", "output JSON path").Required().String()
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
	return json.NewEncoder(fp).Encode(&events)
}

const PageTemplate = `
<html>
<header>
	<meta charset="utf-8">
	<title>Agenda Brest</title>
</header>
<body>
<table>
	{{range .Before}}
	<tr>
		<td><a href="{{.Link}}">link</a></td>
		<td style="white-space:nowrap">{{.Start}}</td>
		<td style="white-space:nowrap">{{.End}}</td>
		<td>{{.Weekday}}</td>
		<td>{{.DeltaStr}}</td>
		<td>{{.Title}}</td>
	</tr>
	{{end}}
	{{if .HasAfter}}
</table>
<hr id="now"></hr>
<table>
	{{end}}
	{{range .After}}
	<tr>
		<td><a href="{{.Link}}">link</a></td>
		<td style="white-space:nowrap">{{.Start}}</td>
		<td style="white-space:nowrap">{{.End}}</td>
		<td>{{.Weekday}}</td>
		<td>{{.DeltaStr}}</td>
		<td>{{.Title}}</td>
	</tr>
	{{end}}
</table>
</body>
</html>
`

type HtmlEntry struct {
	Link     string
	Start    string
	End      string
	DeltaStr string
	Delta    int
	Title    string
	Weekday  string
}

type sortedEntries []HtmlEntry

func (ev sortedEntries) Len() int {
	return len(ev)
}
func (ev sortedEntries) Swap(i, j int) {
	ev[i], ev[j] = ev[j], ev[i]
}
func (ev sortedEntries) Less(i, j int) bool {
	return ev[i].Delta < ev[j].Delta
}

var Weekdays = []string{
	"Di",
	"Lu",
	"Ma",
	"Me",
	"Je",
	"Ve",
	"Sa",
}

func writeHtml(w io.Writer, events []Event) error {
	t, err := template.New("html").Parse(PageTemplate)
	if err != nil {
		return err
	}

	type Entries struct {
		Before   []HtmlEntry
		After    []HtmlEntry
		HasAfter bool
	}

	now := time.Now()
	now = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	entries := Entries{}
	for _, ev := range events {
		baseDate := ev.Start
		mult := 1
		if !ev.Start.After(now) && !ev.End.IsZero() {
			baseDate = ev.End
			mult = -1
		}
		delta := mult * int(baseDate.Sub(now).Hours()/24)
		deltaStr := ""
		if delta != 0 {
			deltaStr = fmt.Sprintf("%+dj", delta)
		}
		entry := HtmlEntry{
			Link:     ev.Link,
			Start:    ev.Start.Format("2006-01-02"),
			DeltaStr: deltaStr,
			Delta:    delta,
			Title:    ev.Title,
			Weekday:  Weekdays[int(baseDate.Weekday())],
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
	sort.Sort(sortedEntries(entries.Before))
	sort.Sort(sortedEntries(entries.After))
	entries.HasAfter = len(entries.Before) > 0 && len(entries.After) > 0
	return t.Execute(w, &entries)
}

func loadEvents(path string) ([]Event, error) {
	fp, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fp.Close()
	events := []Event{}
	err = json.NewDecoder(fp).Decode(&events)
	return events, err
}

var (
	formatCmd     = app.Command("format", "write agent events as HTML")
	formatJsonArg = formatCmd.Arg("json", "input JSON path").Required().String()
	formatPathArg = formatCmd.Arg("path", "output HTML path").Required().String()
)

func formatFn() error {
	outPath := *formatPathArg
	jsonPath := *formatJsonArg
	events, err := loadEvents(jsonPath)
	if err != nil {
		return err
	}
	fp, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer fp.Close()
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
	case formatCmd.FullCommand():
		return formatFn()
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
