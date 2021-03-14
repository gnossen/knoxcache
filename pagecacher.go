package main

import (
    "github.com/gnossen/cache/datastore"
    enc "github.com/gnossen/cache/encoder"
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

// TODO: Get this from config somehow.
// TODO: Change to http://c/
const baseName = "http://localhost:8080/c/"

// TODO: Make storage location configurable.
var ds = datastore.NewFileDatastore("")
var encoder = enc.NewDefaultEncoder()

// TODO: img src
var linkAttrs = map[string][]string{
    "a": []string{"href"},
    "link": []string{"href"},
    "meta": []string{"content"},
    "script": []string{"src"},
    "img": []string{"src"},
}

// TODO: Pull out into separate template file.
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

// TODO: Need a bijective encoding scheme so that we can pre-translate the links,
// and do just-in-time caching.

// URL is assumed to be a normalized absolute URL.

func translateAbsoluteUrlToCachedUrl(toTranslate string) (string, error) {
    encoded, err := encoder.Encode(toTranslate)
    if err != nil {
        return "", nil
    }
    return baseName + encoded, nil
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
                // TODO: Modify the link properly.
                // TODO: Add to queue.
                translated, err := translateCachedUrl(node.Attr[i].Val, baseUrl)
                log.Printf("Cached URL as (%d) %s\n", len(translated), translated)
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
    encodedUrl, err := encoder.Encode(srcUrl)
    if err != nil {
        return "", err
    }
    resp, err := http.Get(srcUrl)
    if err != nil {
        log.Println("Failed to get url %s: %v", srcUrl, err)
        return "", err
    }
    // TODO: Inspect the MIME type and respond appropriately.

    log.Printf("Caching %s as %s\n", srcUrl, encodedUrl)
    outfile, err := ds.Create(encodedUrl)
    if err != nil {
        log.Println("Failed to open page %s for writing: %v", encodedUrl, err)
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
    // TODO: Copy Headers as well.
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
        _, err = cachePage(decodedUrl, ds)
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
            successMsg := fmt.Sprintf("<br />Created <a href=\"%s\">%s</a>\n",
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
