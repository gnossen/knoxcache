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
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TODO: How do we take time slicing into account?

// TODO: Get this from config somehow.
// TODO: Change to http://c/
const baseNameFormat = "http://%s/c/"
const defaultListenHost = "0.0.0.0"
const defaultPort = "8080"

const maxUrlSize = 160

var advertiseAddress = flag.String("advertise-address", "localhost:8080", "The address at which the service will be accessible.")
var listenAddress = flag.String("listen-address", "0.0.0.0:8080", "The address at which the service will listen.")
var datastoreRoot = flag.String("file-store-root", "", "The directory in which to place cached files.")

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
            <p><a href="admin/list">Cached Resources</a></p>
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
        <table>
            <tr>
                <th>Source Page</th>
                <th>Cached Resource</th>
                <th>Cache Date</th>
            </tr>
`

const adminListFooter = `
        </table>
        </div>
		</center>
    </body>
</html>
`

// URL is assumed to be a normalized absolute URL.
func translateAbsoluteUrlToCachedUrl(toTranslate string) (string, error) {
	encoded, err := encoder.Encode(toTranslate)
	if err != nil {
		return "", nil
	}
	return baseName + encoded, nil
}

// Should we do a sha256 hash or what?
func translateCachedUrl(toTranslate string, baseUrl *url.URL) (string, error) {
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
	translated, err := translateAbsoluteUrlToCachedUrl(absoluteUrl.String())
	if err != nil {
		return "", err
	}
	return translated, nil
}

func modifyLink(tag string, node *html.Node, baseUrl *url.URL) {
	for i, attr := range node.Attr {
		for _, linkAttr := range linkAttrs[tag] {
			if attr.Key == linkAttr {
				translated, err := translateCachedUrl(node.Attr[i].Val, baseUrl)
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

func cachePage(srcUrl string, ds datastore.Datastore, userAgent string) (string, error) {
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

	parsedUrl, parseErr := url.Parse(srcUrl)
	if parseErr != nil {
		log.Println("Failed to parse URL %s", parsedUrl)
		return "", parseErr
	}

	contentType := "text/html"
	rawContentType := resp.Header.Get("Content-Type")
	if rawContentType != "" {
		mediaType, _, err := mime.ParseMediaType(rawContentType)
		// If we cannot parse the MIME type, assume HTML.
		if err == nil {
			contentType = mediaType
		}
	}

	for _, filteredHeaderKey := range filteredHeaderKeys {
		if resp.Header.Get(filteredHeaderKey) != "" {
			resp.Header.Del(filteredHeaderKey)
		}
	}

	resourceWriter.WriteHeaders(&resp.Header)

	if contentType == "text/html" {
		// TODO: Do this translation when *serving* pages, not when caching
		// them. This gives us the flexibility to change how we modify them
		// in the future without ever risking having deleted irretrievable
		// information.
		var visitNode func(node *html.Node)
		visitNode = func(node *html.Node) {
			if node.Type == html.ElementNode {
				if _, ok := linkAttrs[node.Data]; ok {
					modifyLink(node.Data, node, parsedUrl)
				}
			}
			for c := node.FirstChild; c != nil; c = c.NextSibling {
				visitNode(c)
			}
		}

		doc, err := html.Parse(resp.Body)
		if err != nil {
			return "", err
		}

		err = addInterceptionScript(doc)
		if err != nil {
			return "", err
		}

		visitNode(doc)
		html.Render(resourceWriter, doc)
	} else {
		log.Printf("  Saving as %s\n", contentType)
		// TODO: Actually check for errors here.
		io.Copy(resourceWriter, resp.Body)
	}
	// TODO: Add special handler for CSS that parses and replaces references.

	translated, err := translateAbsoluteUrlToCachedUrl(srcUrl)
	if err != nil {
		return "", err
	}
	return translated, nil
}

func serveExistingPage(encodedUrl string, w http.ResponseWriter) {
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
	io.Copy(w, f)
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

	exists, err := ds.Exists(encodedUrl)

	if err != nil {
		msg := fmt.Sprintf("Internal error: %v\n", err)
		w.WriteHeader(500)
		io.WriteString(w, msg)
		return
	}

	if !exists {
		decodedUrl, err := encoder.Decode(encodedUrl)
		if err != nil {
			msg := fmt.Sprintf("Could not interpret requested url '%s'", encodedUrl)
			w.WriteHeader(400)
			io.WriteString(w, msg)
			return
		}
		_, err = cachePage(decodedUrl, ds, r.Header.Get("User-Agent"))
		if err != nil {
			// TODO: Figure out how to dedupe this.
			w.WriteHeader(500)
			msg := fmt.Sprintf("Failed to cache page: %v", err)
			io.WriteString(w, msg)
			return
		}
		serveExistingPage(encodedUrl, w)
		return
	} else {
		serveExistingPage(encodedUrl, w)
		return
	}
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
			cachedUrl, err := cachePage(requestedUrl, ds, r.Header.Get("User-Agent"))
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

func handleListRequest(w http.ResponseWriter, r *http.Request) {
	ri, err := ds.List()
	if err != nil {
		log.Printf("Failed to list resources: %v\n", err)
	}

	io.WriteString(w, "{\n  \"resources\": [\n")
	for ri.HasNext() {
		metadata, err := ri.Next()
		if err != nil {
			log.Printf("failed to list entry: %v\n", err)
			continue
		}
		url := metadata.Url
		io.WriteString(w, "    \"")
		io.WriteString(w, url)
		if ri.HasNext() {
			io.WriteString(w, "\",\n")
		} else {
			io.WriteString(w, "\"\n")
		}
	}
	io.WriteString(w, "  ]\n}\n")
}

func shortenedUrl(url string) string {
	if len(url) <= maxUrlSize {
		return url
	}
	return url[0:maxUrlSize] + "..."
}

func handleAdminListRequest(w http.ResponseWriter, r *http.Request) {
	// TODO: Figure out a way to write resource count and total size at
	// beginning without first having to iterate through the whole thing.
	ri, err := ds.List()
	if err != nil {
		log.Printf("Failed to list resources: %v\n", err)
	}
	io.WriteString(w, adminListHeader)
	for ri.HasNext() {
		metadata, err := ri.Next()
		if err != nil {
			log.Printf("failed to list entry: %v\n", err)
			continue
		}
		url := metadata.Url
		translatedUrl, err := translateAbsoluteUrlToCachedUrl(url)
		if err != nil {
			log.Printf("failed to get cached URL for %s: %v\n", url, err)
			continue
		}
		io.WriteString(w, "<tr>")
		io.WriteString(w, fmt.Sprintf("<td class=\"source-url\"><a href=\"%s\">%s</a></td>\n", url, shortenedUrl(url)))
		io.WriteString(w, fmt.Sprintf("<td><a href=\"%s\">Cached</a></td>\n", translatedUrl))
		io.WriteString(w, fmt.Sprintf("<td>%s</td>\n", metadata.CreationTime.Format(time.UnixDate)))
		io.WriteString(w, "</tr>")
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
	ds = datastore.NewFileDatastore(*datastoreRoot)
	http.HandleFunc("/", handleCreatePageRequest)
	http.HandleFunc("/c/", handlePageRequest)
	http.HandleFunc("/list", handleListRequest)
	http.HandleFunc("/admin/list", handleAdminListRequest)
	http.HandleFunc("/service-worker.js", handleServiceWorker)
	baseName = fmt.Sprintf(baseNameFormat, *advertiseAddress)
	log.Printf("Listening on %s", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
