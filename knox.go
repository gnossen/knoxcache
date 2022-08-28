package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/gnossen/knoxcache/datastore"
	enc "github.com/gnossen/knoxcache/encoder"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TODO: How do we take time slicing into account?

const defaultListenHost = "0.0.0.0"
const defaultPort = "8080"

const maxUrlDisplaySize = 160

const maxResourcesPerPage = 100

var adminListRegex *regexp.Regexp

var advertiseAddress = flag.String("advertise-address", "localhost:8080", "The address at which the service will be accessible.")
var listenAddress = flag.String("listen-address", "0.0.0.0:8080", "The address at which the service will listen.")
var datastoreRoot = flag.String("file-store-root", "", "The directory in which to place cached files.")
var dbFile = flag.String("db-file", "", "The path to the sqlite db file.")

var baseName = ""

var ds datastore.FileDatastore
var encoder = enc.NewDefaultEncoder()

var linkAttrs = map[string][]string{
	"a":      []string{"href"},
	"link":   []string{"href"},
	"meta":   []string{"content"},
	"script": []string{"src"},
	"img":    []string{"src"},
}

var filteredHeaderKeys = []string{
	"Content-Length",
	"Alt-Svc",
	"Date",
	"Strict-Transport-Security",
	"Via",
}

// TODO: Pull out into separate template file.
// TODO: Make pretty.
const headerText = `
<html>
    <title>Knox Cache</title>
    <body>
`

const createPageFormText = `
        <style>
        .input-form {
            position: fixed;
            left: 0;
            top: 20%;
            width: 100%;
            text-align: center;
        }
		body {
		  font-family: Sans-Serif;
		}
        </style>
        <div class="input-form">
            <form>
                <input type="text" size="80" name="url"><br /><br />
                <input type="submit" value="Create">
            </form>
`

const ipFooterFormatText = `
        </div>

        <style>
        .footer {
          position: fixed;
          left: 0;
          bottom: 0;
          width: 100%%;
          text-align: center;
        }
        </style>

        <div class="footer">
            <p><a href="admin/list/0">Cached Resources</a></p>
            <p>Served from %s</p>
        </div>
`

const footerText = `
    </body>
</html>
`

// TODO: Link script files into binary instead of textually embedding.
const interceptionScript = `
if ('serviceWorker' in navigator) {
    window.addEventListener('load', function() {
        navigator.serviceWorker.register('../service-worker.js').then(function(registration){
            console.log("Service worker registered with scope: ", registration.scope);
        }, function(err) {
            console.log("Service worker registration failed: ", err);
        });
    });
}
`

const interceptionServiceWorkerFormat = `
self.addEventListener('fetch', function(event) {
    var advertisedAddress = "%s";
    var pattern = /^https?:\/\//i;
    if (pattern.test(event.request.url) && event.request.url.lastIndexOf("http://" + advertisedAddress) != 0) {
        // Absolute URLs are simple to replace.
        var newUrl = "http://" + advertisedAddress + "/c/" + btoa(event.request.url);
        event.request.url = newUrl;
        event.respondWith(fetch(event.request));
    } else {
        console.log("Failed to intercept relative URL: ", event.request.url)
    }
});
`

// TODO: Dedupe some of this CSS.
// TODO: Add doctype to everything.
// TODO: Dark mode.

const adminListHeader = `
<!DOCTYPE html>
<html>
    <style>
        body {
		  font-family: Sans-Serif;
        }
        table {
          width: 80%;
        }   
		table, th, td {
		  border: 1px solid black;
		  border-collapse: collapse;
		  padding: 4px;
		  white-space: nowrap;
		}
        td {
          padding-top: 0.5vh;
          padding-bottom: 0.5vh;
        }
        .source-url {
		  overflow: hidden;
          overflow-x: hidden;
		  text-overflow: ellipsis;
		  -o-text-overflow: ellipsis;
        }
    </style>
    <head>
        <title>Knox Admin List</title>
    </head>
    <body>
		<center>
        <div style="overflow-x: auto;">
`

const globalStatsTableHeader = `
        <table>
            <tr>
                <th>Resource Count</th>
                <th>Disk Usage</th>
            </tr>
`

const globalStatsTableFooter = `
        </table>
        <br />
`

const resourceListTableHeader = `
        <table>
            <tr>
                <th>Source Page</th>
                <th>Cached Resource</th>
                <th>Download Initiated</th>
                <th>Download Duration</th>
                <th>Original Size</th>
                <th>Size on Disk</th>
            </tr>
`

const adminListFooter = `
		</center>
    </body>
</html>
`

var dataSizeUnits []string = []string{
	"B",
	"KB",
	"MB",
	"GB",
	"TB",
	"PB",
}

func formatUnit(magnitude float64, unit string) string {
	magnitudeString := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", magnitude), "0"), ".")
	return magnitudeString + unit
}

func formatDataSize(bytes int) string {
	currentUnitSize := float64(bytes)
	for unitIndex := 0; unitIndex < len(dataSizeUnits)-1; unitIndex += 1 {
		if currentUnitSize < 1024.0 {
			return formatUnit(currentUnitSize, dataSizeUnits[unitIndex])
		}
		currentUnitSize = currentUnitSize / 1024.0
	}
	return formatUnit(currentUnitSize, dataSizeUnits[len(dataSizeUnits)-1])
}

// URL is assumed to be a normalized absolute URL.
func translateAbsoluteUrlToCachedUrl(toTranslate string, protocol string, host string) (string, error) {
	encoded, err := encoder.Encode(toTranslate)
	if err != nil {
		return "", nil
	}
	return fmt.Sprintf("%s://%s/c/%s", protocol, host, encoded), nil
}

func translateCachedUrl(toTranslate string, baseUrl *url.URL, protocol string, host string) (string, error) {
	parsedUrl, err := url.Parse(toTranslate)
	if err != nil {
		return "", err
	}
	var absoluteUrl *url.URL
	if !parsedUrl.IsAbs() {
		absoluteUrl = baseUrl.ResolveReference(parsedUrl)
	} else {
		absoluteUrl = parsedUrl
	}
	translated, err := translateAbsoluteUrlToCachedUrl(absoluteUrl.String(), protocol, host)
	if err != nil {
		return "", err
	}
	return translated, nil
}

func modifyLink(tag string, node *html.Node, baseUrl *url.URL, protocol string, host string) {
	for i, attr := range node.Attr {
		for _, linkAttr := range linkAttrs[tag] {
			if attr.Key == linkAttr {
				translated, err := translateCachedUrl(node.Attr[i].Val, baseUrl, protocol, host)
				if err != nil {
					fmt.Println("Failed to parse as URL.")
					continue
				}
				node.Attr[i].Val = translated
			}
		}
	}
}

func addInterceptionScript(doc *html.Node) error {
	// TODO: Inject `<script>$SCRIPT</script>`.
	scriptNode := &html.Node{
		nil, nil, nil, nil, nil,
		html.ElementNode, atom.Script,
		"script", "", []html.Attribute{},
	}
	scriptTextNode := &html.Node{
		nil, nil, nil, nil, nil,
		html.TextNode, atom.Plaintext,
		interceptionScript, "", []html.Attribute{},
	}
	scriptNode.AppendChild(scriptTextNode)
	doc.InsertBefore(scriptNode, doc.FirstChild)
	return nil
}

func getContentType(headers *http.Header) string {
	contentType := "text/html"
	rawContentType := headers.Get("Content-Type")
	if rawContentType != "" {
		mediaType, _, err := mime.ParseMediaType(rawContentType)
		// If we cannot parse the MIME type, assume HTML.
		if err == nil {
			contentType = mediaType
		}
	}

	return contentType
}

// TODO: Cache the transformation if it becomes a bottleneck.
func transformHtml(resourceUrl *url.URL, in io.Reader, out io.Writer, protocol string, host string) error {
	var visitNode func(node *html.Node)
	visitNode = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if _, ok := linkAttrs[node.Data]; ok {
				modifyLink(node.Data, node, resourceUrl, protocol, host)
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			visitNode(c)
		}
	}

	doc, err := html.Parse(in)
	if err != nil {
		return err
	}

	err = addInterceptionScript(doc)
	if err != nil {
		return err
	}

	visitNode(doc)
	html.Render(out, doc)

	return nil
}

func cachePage(srcUrl string, ds datastore.Datastore, userAgent string, protocol string, host string) (string, error) {
	encodedUrl, err := encoder.Encode(srcUrl)
	if err != nil {
		return "", err
	}
	client := &http.Client{}
	req, err := http.NewRequest("GET", srcUrl, nil)
	if err != nil {
		return "", err
	}
	if userAgent != "" {
		req.Header.Add("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to get url %s: %v\n", srcUrl, err)
		return "", err
	}

	log.Printf("Caching %s as %s\n", srcUrl, encodedUrl)
	resourceWriter, err := ds.Create(srcUrl, encodedUrl)
	if err != nil {
		log.Println("Failed to open page %s for writing: %v", encodedUrl, err)
		return "", err
	}
	defer resourceWriter.Close()

	for _, filteredHeaderKey := range filteredHeaderKeys {
		if resp.Header.Get(filteredHeaderKey) != "" {
			resp.Header.Del(filteredHeaderKey)
		}
	}

	resourceWriter.WriteHeaders(&resp.Header)

	if _, err = io.Copy(resourceWriter, resp.Body); err != nil {
		return "", err
	}

	translated, err := translateAbsoluteUrlToCachedUrl(srcUrl, protocol, host)
	if err != nil {
		return "", err
	}
	return translated, nil
}

func serveExistingPage(encodedUrl string, w http.ResponseWriter, protocol string, host string) {
	f, openErr := ds.Open(encodedUrl)
	if openErr != nil {
		log.Printf("Failed to open file for hash %s: %v", encodedUrl, openErr)
		msg := fmt.Sprintf("Internal error: %v\n", openErr)
		w.WriteHeader(500)
		io.WriteString(w, msg)
		return
	}
	defer f.Close()
	decodedUrl, _ := encoder.Decode(encodedUrl)
	log.Printf("Serving %s (%s)\n", decodedUrl, encodedUrl)
	for key, values := range *f.Headers() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	parsedUrl, parseErr := url.Parse(f.ResourceURL())
	if parseErr != nil {
		log.Println("Failed to parse URL %s: %v", parsedUrl, parseErr)
		w.WriteHeader(400)
		io.WriteString(w, fmt.Sprintf("Bad URL: %v", parseErr))
		return
	}

	// Transform the page.
	contentType := getContentType(f.Headers())
	if contentType == "text/html" {
		if err := transformHtml(parsedUrl, f, w, protocol, host); err != nil {
			log.Println("Failed to transform HTML: %v", err)
			w.WriteHeader(500)
			io.WriteString(w, fmt.Sprintf("Failed to transform HTML: %v", err))
			return
		}
	} else {
		_, err := io.Copy(w, f)
		if err != nil {
			log.Println("Error serving '%s': %v", f.ResourceURL(), err)
		}
	}
}

func getProtocol(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	return "http"
}

func getHost(r *http.Request) string {
	if hostHeader := r.Host; hostHeader != "" {
		return hostHeader
	}
	return baseName
}

func handlePageRequest(w http.ResponseWriter, r *http.Request) {
	// Strip the slash
	prefix := "/c/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(400)
		io.WriteString(w, "Bad URI.")
		return
	}
	encodedUrl := r.URL.Path[len(prefix):]

	status, err := ds.Status(encodedUrl)

	if err != nil {
		msg := fmt.Sprintf("Internal error: %v\n", err)
		w.WriteHeader(500)
		io.WriteString(w, msg)
		return
	}

	if status == datastore.ResourceDownloading {
		w.WriteHeader(409)
		io.WriteString(w, "Resource downloading")
		return
	}

	if status == datastore.ResourceNotCached {
		decodedUrl, err := encoder.Decode(encodedUrl)
		if err != nil {
			msg := fmt.Sprintf("Could not interpret requested url '%s'", encodedUrl)
			w.WriteHeader(400)
			io.WriteString(w, msg)
			return
		}
		_, err = cachePage(decodedUrl, ds, r.Header.Get("User-Agent"), getProtocol(r), getHost(r))
		if err != nil {
			// TODO: Figure out how to dedupe this.
			w.WriteHeader(500)
			msg := fmt.Sprintf("Failed to cache page: %v", err)
			io.WriteString(w, msg)
			return
		}
	}

	serveExistingPage(encodedUrl, w, getProtocol(r), getHost(r))
	return
}

func queryError(w http.ResponseWriter) {
	w.WriteHeader(400)
	io.WriteString(w, "Invalid query.")
}

func writeFooter(w http.ResponseWriter, context context.Context) {
	localAddr := context.Value(http.LocalAddrContextKey)
	io.WriteString(w, fmt.Sprintf(ipFooterFormatText, localAddr))
	io.WriteString(w, footerText)
}

func handleCreatePageRequest(w http.ResponseWriter, r *http.Request) {
	queries := r.URL.Query()
	if len(queries) == 0 {
		w.WriteHeader(200)
		io.WriteString(w, headerText)
		io.WriteString(w, createPageFormText)
		writeFooter(w, r.Context())
		return
	} else if len(queries) == 1 {
		requestedUrls, ok := queries["url"]
		if !ok || len(requestedUrls) != 1 {
			queryError(w)
			return
		} else {
			requestedUrl := requestedUrls[0]
			cachedUrl, err := cachePage(requestedUrl, ds, r.Header.Get("User-Agent"), getProtocol(r), getHost(r))
			if err != nil {
				w.WriteHeader(500)
				msg := fmt.Sprintf("Failed to cache page: %v", err)
				io.WriteString(w, msg)
				return
			}
			successMsg := fmt.Sprintf("<br />Created <a href=\"%s\">%s</a>\n",
				cachedUrl, cachedUrl)
			w.WriteHeader(200)
			io.WriteString(w, headerText)
			io.WriteString(w, createPageFormText)
			io.WriteString(w, successMsg)
			writeFooter(w, r.Context())
		}
	} else {
		queryError(w)
		return
	}
}

func shortenedUrl(url string) string {
	if len(url) <= maxUrlDisplaySize {
		return url
	}
	return url[0:maxUrlDisplaySize] + "..."
}

func handleAdminListRequest(w http.ResponseWriter, r *http.Request) {
	// TODO: Figure out a way to write resource count and total size at
	// beginning without first having to iterate through the whole thing.

	if !adminListRegex.MatchString(r.URL.Path) {
		w.WriteHeader(400)
		io.WriteString(w, fmt.Sprintf("Bad URI: %s", r.URL.Path))
		return
	}

	pageNumStr := adminListRegex.FindStringSubmatch(r.URL.Path)[1]
	pageNum, err := strconv.Atoi(pageNumStr)
	if err != nil {
		log.Printf("%v", adminListRegex)
		w.WriteHeader(500)
		io.WriteString(w, fmt.Sprintf("Internal error: %v", err))
		return
	}
	stats, err := ds.Stats()
	if err != nil {
		msg := fmt.Sprintf("Failed to get global stats: %v\n", err)
		log.Printf(msg)
		w.WriteHeader(500)
		io.WriteString(w, msg)
	}
	ri, err := ds.List(pageNum*maxResourcesPerPage, maxResourcesPerPage)
	if err != nil {
		msg := fmt.Sprintf("Failed to list resources: %v\n", err)
		log.Printf(msg)
		w.WriteHeader(500)
		io.WriteString(w, msg)
	}
	io.WriteString(w, adminListHeader)
	io.WriteString(w, globalStatsTableHeader)
	io.WriteString(w, "<tr>")
	io.WriteString(w, fmt.Sprintf("<td>%d</td>", stats.RecordCount))
	io.WriteString(w, fmt.Sprintf("<td>%s</td>", formatDataSize(stats.DiskConsumptionBytes)))
	io.WriteString(w, "</tr>")
	io.WriteString(w, globalStatsTableFooter)
	io.WriteString(w, resourceListTableHeader)
	resourceCount := 0
	for ri.HasNext() {
		metadata, err := ri.Next()
		if err != nil {
			log.Printf("failed to list entry: %v\n", err)
			continue
		}
		url := metadata.Url
		translatedUrl, err := translateAbsoluteUrlToCachedUrl(url, getProtocol(r), getHost(r))
		if err != nil {
			log.Printf("failed to get cached URL for %s: %v\n", url, err)
			continue
		}
		io.WriteString(w, "<tr>")
		io.WriteString(w, fmt.Sprintf("<td class=\"source-url\"><a href=\"%s\">%s</a></td>\n", url, shortenedUrl(url)))
		io.WriteString(w, fmt.Sprintf("<td><a href=\"%s\">Cached</a></td>\n", translatedUrl))
		io.WriteString(w, fmt.Sprintf("<td>%s</td>\n", metadata.DownloadStarted.Format(time.UnixDate)))

		io.WriteString(w, fmt.Sprintf("<td>%s</td>\n", metadata.DownloadDuration.String()))
		io.WriteString(w, fmt.Sprintf("<td>%s</td>\n", formatDataSize(metadata.RawBytes)))
		io.WriteString(w, fmt.Sprintf("<td>%s</td>\n", formatDataSize(metadata.BytesOnDisk)))

		io.WriteString(w, "</tr>")
		resourceCount += 1
	}
	io.WriteString(w, "</table></div><br />")

	noMoreResources := (resourceCount != maxResourcesPerPage)

	if pageNum != 0 {
		io.WriteString(w, fmt.Sprintf("<a href=\"/admin/list/%d\">&lt; previous</a> &nbsp;&nbsp;", pageNum-1))
	}

	if !noMoreResources {
		io.WriteString(w, fmt.Sprintf("<a href=\"/admin/list/%d\">next &gt;</a>", pageNum+1))
	}
	io.WriteString(w, adminListFooter)
}

func handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/javascript")
	// TODO: Only evaluate this template once.
	io.WriteString(w, fmt.Sprintf(interceptionServiceWorkerFormat, *advertiseAddress))
}

func main() {
	flag.Parse()
	var err error
	actualDbFile := *dbFile
	if actualDbFile == "" {
		actualDbFile = path.Join(*datastoreRoot, "knox.db")
	}
	ds, err = datastore.NewFileDatastore(actualDbFile, *datastoreRoot)
	if err != nil {
		panic(err)
	}
	http.HandleFunc("/", handleCreatePageRequest)
	http.HandleFunc("/c/", handlePageRequest)
	http.HandleFunc("/admin/list/", handleAdminListRequest)
	http.HandleFunc("/service-worker.js", handleServiceWorker)

	adminListRegex, err = regexp.Compile("^/admin/list/([0-9]+)$")
	if err != nil {
		panic(fmt.Sprintf("Failed to compile /admin/list regex: %v", err))
	}

	baseName = *advertiseAddress
	srv := &http.Server{Addr: *listenAddress, Handler: nil}
	ln, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		panic(fmt.Sprintf("Failed to listen on %s: %v", *listenAddress, err))
	}
	log.Printf("Listening on %s", ln.Addr().String())
	log.Fatal(srv.Serve(ln))
}
