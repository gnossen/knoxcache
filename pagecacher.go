package main

import (
    "github.com/gnossen/cache/datastore"
    "crypto/sha256"
    "encoding/base32"
    "net/http"
    "net/url"
    "golang.org/x/net/html"
    "io"
    "strings"
    "fmt"
    "log"
)

// TODO: How do we take time slicing into account?

const mainUrl = "https://docs.openshift.com/container-platform/3.4/install_config/upgrading/manual_upgrades.html"

// TODO: Programmatically generate.
const outFileName = "cached.html"

// TODO: Get this from config somehow.
// TODO: Change to http://c/
const baseName = "http://localhost:8080/c/"

// TODO: Make storage location configurable.
var ds = datastore.NewFileDatastore("")

var linkAttrs = map[string][]string{
    "a": []string{"href"},
    "link": []string{"href"},
    "meta": []string{"content"},
    "script": []string{"src"},
}

// TODO: Make pretty.
const headerText = `
<html>
    <title>Cache</title>
    <body>
`

const createPageFormText = `
        <form>
            <input type="text" name="url">
            <input type="submit" value="Create">
        </form>
`

const footerText = `
    </body>
</html>
`

const createPageText = headerText + createPageFormText + footerText;

// URL is assumed to be a normalized absolute URL.
func hashUrl(url string) string {
    bytes := sha256.Sum256([]byte(url))
    rawEncoding :=  base32.StdEncoding.EncodeToString(bytes[:])
    return strings.ToLower(rawEncoding[:len(rawEncoding)-4])
}

func translateAbsoluteUrlToCachedUrl(toTranslate string) string {
    return baseName + hashUrl(toTranslate)
}

// Should we do a sha256 hash or what?
func translateCachedUrl(toTranslate string, baseUrl *url.URL ) (string, error) {
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
    return translateAbsoluteUrlToCachedUrl(absoluteUrl.String()), nil
}

func modifyLink(tag string, node *html.Node, baseUrl *url.URL) {
    for i, attr := range node.Attr {
        for _, linkAttr := range linkAttrs[tag] {
            if attr.Key == linkAttr {
                // TODO: Modify the link properly.
                // TODO: Add to queue.
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

func cachePage(srcUrl string, ds datastore.Datastore) (string, error) {
    hashedUrl := hashUrl(srcUrl)
    resp, err := http.Get(srcUrl)
    if err != nil {
        log.Println("Failed to get url %s: %v", srcUrl, err)
        return "", err
    }
    // TODO: Inspect the MIME type and respond appropriately.

    log.Printf("Caching %s as %s\n", srcUrl, hashedUrl)
    outfile, err := ds.Create(hashedUrl)
    if err != nil {
        log.Println("Failed to open file %s: %v", outFileName, err)
        return "", err
    }
    defer outfile.Close()

    parsedUrl, parseErr := url.Parse(srcUrl)
    if parseErr != nil {
        log.Println("Failed to parse URL %s", parsedUrl)
        return "", parseErr
    }

    // Gathers other links that need to be fetched and modifies their hyperlinks.
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
        log.Println("Failed to parse HTML: %v", err)
        return "", err
    }
    visitNode(doc)
    html.Render(outfile, doc)
    return translateAbsoluteUrlToCachedUrl(srcUrl), nil
}

func handlePageRequest(w http.ResponseWriter, r *http.Request) {
    // Strip the slash
    prefix := "/c/"
    if !strings.HasPrefix(r.URL.Path, prefix) {
        w.WriteHeader(400)
        io.WriteString(w, "Bad URI.")
        return
    }
    hashedUrl := r.URL.Path[len(prefix):]

    // TODO: Try to accept short links.

    exists, err := ds.Exists(hashedUrl)

    if err != nil {
        msg := fmt.Sprintf("Internal error: %v\n", err)
        w.WriteHeader(500)
        io.WriteString(w, msg)
        return
    }

    if !exists {
        // TODO: Something prettier than this.
        w.WriteHeader(404)
        io.WriteString(w, "<h1>Not Found.</h1>\n")
        return
    }

    f, openErr := ds.Open(hashedUrl)
    if openErr != nil {
        log.Printf("Failed to open file for hash %s: %v", hashedUrl, openErr)
        msg := fmt.Sprintf("Internal error: %v\n", openErr)
        w.WriteHeader(500)
        io.WriteString(w, msg)
        return
    }
    defer f.Close()
    io.Copy(w, f)
}

func queryError(w http.ResponseWriter) {
    w.WriteHeader(400)
    io.WriteString(w, "Invalid query.")
}

func handleCreatePageRequest(w http.ResponseWriter, r *http.Request) {
    queries := r.URL.Query()
    if len(queries) == 0 {
        w.WriteHeader(200)
        io.WriteString(w, createPageText)
        return
    } else if len(queries) == 1 {
        requestedUrls, ok := queries["url"]
        if !ok || len(requestedUrls) != 1{
            queryError(w)
            return
        } else {
            requestedUrl := requestedUrls[0]
            cachedUrl, err := cachePage(requestedUrl, ds)
            if err != nil {
                w.WriteHeader(500)
                msg := fmt.Sprintf("Failed to cache page: %v", err)
                io.WriteString(w, msg)
                return
            }
            successMsg := fmt.Sprintf("<br />.Created <a href=\"%s\">%s</a>\n",
                cachedUrl, cachedUrl)
            w.WriteHeader(200)
            io.WriteString(w, headerText)
            io.WriteString(w, createPageFormText)
            io.WriteString(w, successMsg)
            io.WriteString(w, footerText)
        }
    } else {
        queryError(w)
        return
    }
}

func main() {
    http.HandleFunc("/", handleCreatePageRequest)
    http.HandleFunc("/c/", handlePageRequest)
    log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
