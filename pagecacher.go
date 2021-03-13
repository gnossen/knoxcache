package main

import (
    "github.com/gnossen/cache/datastore"
    "crypto/sha256"
    "encoding/base32"
    "net/http"
    "net/url"
    "golang.org/x/net/html"
    "os"
    "strings"
    "fmt"
)

// TODO: How do we take time slicing into account?

const mainUrl = "https://docs.openshift.com/container-platform/3.4/install_config/upgrading/manual_upgrades.html"

// TODO: Programmatically generate.
const outFileName = "cached.html"

// TODO: Get this from config somehow.
const baseName = "http://c/"

var linkAttrs = map[string][]string{
    "a": []string{"href"},
    "link": []string{"href"},
    "meta": []string{"content"},
    "script": []string{"src"},
}

// URL is assumed to be a normalized absolute URL.
func hashUrl(url string) string {
    fmt.Println("Hashing URL ", url)
    bytes := sha256.Sum256([]byte(url))
    rawEncoding :=  base32.StdEncoding.EncodeToString(bytes[:])
    return strings.ToLower(rawEncoding[:len(rawEncoding)-4])
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
    return baseName + hashUrl(absoluteUrl.String()), nil
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

// TODO: Return err.
func cachePage(urlToCache string, ds datastore.Datastore) {
    resp, err := http.Get(urlToCache)
    if err != nil {
        println("Failed to get url %s: %v", urlToCache, err)
        os.Exit(1)
    }
    // TODO: Inspect the MIME type and respond appropriately.

    // TODO: Make sure it doesn't exist first.
    outfile, err := ds.Create(outFileName)
    if err != nil {
        println("Failed to open file %s: %v", outFileName, err)
        os.Exit(1)
    }
    defer outfile.Close()

    parsedUrl, parseErr := url.Parse(urlToCache)
    if parseErr != nil {
        fmt.Println("Failed to parse URL %s", parsedUrl)
        os.Exit(1)
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
        println("Failed to parse HTML: %v", err)
        os.Exit(1)
    }
    visitNode(doc)
    html.Render(outfile, doc)
}

func main() {
    ds := datastore.NewFileDatastore("./")
    cachePage(mainUrl, ds)
}
