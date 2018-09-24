package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

func extractEvents(doc *goquery.Document, baseUrl *url.URL) ([]Event, error) {
	timeLayout := "2006-01-02"

	events := []Event{}
	items := doc.Find("article[class~='listItem']")
	for i := range items.Nodes {
		item := items.Eq(i)
		title := item.Find("h3[class~='title']").First().Text()
		desc := item.Find("div[class~='chapeau']").First().Text()
		cat := item.Find("p[class='category']").First().Text()
		link, _ := item.Find("a[class='linkView']").First().Attr("href")
		relUrl, err := url.Parse(link)
		if err != nil {
			return nil, err
		}
		relUrl = baseUrl.ResolveReference(relUrl)
		dates := item.Find("p[class='date'] time")
		start, _ := dates.Eq(0).Attr("datetime")
		end, _ := dates.Eq(1).Attr("datetime")
		ev := Event{
			Title:    strings.TrimSpace(title),
			Desc:     strings.TrimSpace(desc),
			Category: strings.TrimSpace(cat),
			Link:     relUrl.String(),
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

var ServerError = fmt.Errorf("server error")

type Page struct {
	Events []Event
	Next   string
}

func getPage(client *http.Client, url, base *url.URL, dumpDir string, pageNum int) (
	*Page, error) {

	rsp, err := client.Get(url.String())
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != 200 {
		if rsp.StatusCode == 500 {
			// Cannot do anything about it, try to generate with what we got.
			return nil, ServerError
		}
		return nil, fmt.Errorf("GET got %d", rsp.StatusCode)
	}
	var r io.Reader = rsp.Body
	if dumpDir != "" {
		path := filepath.Join(dumpDir, fmt.Sprintf("%d.html", pageNum))
		fmt.Println("writing", path)
		data, err := ioutil.ReadAll(rsp.Body)
		if err != nil {
			return nil, err
		}
		err = ioutil.WriteFile(path, data, 0644)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, err
	}
	events, err := extractEvents(doc, base)
	if err != nil {
		return nil, err
	}
	s := doc.Find("link[rel='next']").First()
	u, _ := s.Attr("href")
	page := Page{
		Events: events,
		Next:   u,
	}
	return &page, nil
}

var (
	crawlCmd     = app.Command("crawl", "crawl brest.fr agenda")
	crawlPathArg = crawlCmd.Arg("path", "output JSON path").Required().String()
	crawlDumpDir = crawlCmd.Flag("dump-dir", "optionally dump page content").String()
)

func crawlFn() error {
	outPath := *crawlPathArg
	dumpDir := ""
	if crawlDumpDir != nil {
		dumpDir = *crawlDumpDir
		err := os.MkdirAll(dumpDir, 0755)
		if err != nil {
			return err
		}
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	baseUrl, err := url.Parse("https://www.brest.fr")
	if err != nil {
		return err
	}
	path := "/actus-agenda/agenda-132.html"
	events := []Event{}
	pages := 0
	for {
		u, err := url.Parse(path)
		if err != nil {
			return err
		}
		u = baseUrl.ResolveReference(u)
		fmt.Println("GET", u)
		p, err := getPage(client, u, baseUrl, dumpDir, pages)
		if err != nil {
			if err == ServerError {
				// Ignore 500 errors for now. There is one happening at each
				// crawl and I cannot do anything about it.
				break
			}
			return err
		}
		path = p.Next
		events = append(events, p.Events...)
		if path == "" {
			break
		}
		pages += 1
	}
	if len(events) == 0 {
		return fmt.Errorf("no event found")
	}
	fp, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer fp.Close()
	err = json.NewEncoder(fp).Encode(&events)
	if err != nil {
		return err
	}
	return nil
}

const PageTemplate = `
<html>
<header>
	<meta charset="utf-8">
	<title>Agenda Brest</title>
	<style>
	a:link {
		text-decoration: none;
	}

	a:visited {
		text-decoration: none;
	}

	a:hover {
		text-decoration: underline;
	}

	a:active {
			text-decoration: underline;
	}
	</style>
</header>
<body>
<table>
	{{range .Before}}
	<tr>
		<td style="white-space:nowrap">{{.Start}}</td>
		<td>→</td>
		<td style="white-space:nowrap">{{.End}}</td>
		<td>[{{.Weekday}}]</td>
		<td>{{.DeltaStr}}</td>
		<td><a href="{{.Link}}">{{.Title}}</a></td>
	</tr>
	{{end}}
	{{if .HasAfter}}
</table>
<hr id="now"></hr>
<table>
	{{end}}
	{{range .After}}
	<tr>
		<td style="white-space:nowrap">{{.Start}}</td>
		<td>→</td>
		<td style="white-space:nowrap">{{.End}}</td>
		<td>[{{.Weekday}}]</td>
		<td>{{.DeltaStr}}</td>
		<td><a href="{{.Link}}">{{.Title}}</a></td>
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

func formatDuration(days int) string {
	if days > 30 || days < -30 {
		return fmt.Sprintf("%+dm", days/30)
	} else {
		return fmt.Sprintf("%+dj", days)
	}
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
		startDate := ev.Start
		endDate := ev.Start.Add(24 * time.Hour)
		if !ev.End.IsZero() {
			endDate = ev.End.Add(24 * time.Hour)
		}
		endDateIn := endDate.Add(-24 * time.Hour)
		if !now.Before(endDate) {
			continue
		}
		baseDate := startDate
		relDate := baseDate
		mult := 1
		if startDate.Before(now) {
			relDate = endDate
			baseDate = endDateIn
			mult = -1
		}
		delta := mult * int(relDate.Sub(now).Hours()/24)
		deltaStr := ""
		if delta != 0 {
			deltaStr = formatDuration(delta)
		}
		entry := HtmlEntry{
			Link:     ev.Link,
			Start:    ev.Start.Format("2006-01-02"),
			End:      endDateIn.Format("2006-01-02"),
			DeltaStr: deltaStr,
			Delta:    delta,
			Title:    ev.Title,
			Weekday:  Weekdays[int(relDate.Weekday())],
		}
		if !startDate.Before(now) {
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
