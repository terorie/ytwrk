package apijson

import (
	"github.com/valyala/fastjson"
	"errors"
	"strings"
	"net/http"
	"io/ioutil"
	"fmt"
)

var MissingData = errors.New("missing data")
var ServerError = errors.New("server error")

func ParseChannelVideoURLs(res *http.Response) ([]string, error) {
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP error: %s", res.Request.URL.String())
	}

	// Download response
	defer res.Body.Close()
	buf, err := ioutil.ReadAll(res.Body)
	if err != nil { return nil, err }

	// Parse JSON
	var p fastjson.Parser
	rootObj, err := p.ParseBytes(buf)
	if err != nil { return nil, err }

	// Root as array
	root, err := rootObj.Array()
	if err != nil { return nil, err }

	// Find response container
	var container *fastjson.Value
	for _, item := range root {
		if item.Exists("response") {
			container = item
			break
		}
	}
	if container == nil { return nil, MissingData }

	// Get error obj
	errorExists := container.Exists(
		"response",
		"responseContext",
		"errors",
		"error",
	)
	if errorExists { return nil, ServerError }

	// Get items from grid
	itemsObj := container.Get(
		"response",
		"continuationContents",
		"gridContinuation",
		"items",
	)
	if itemsObj == nil { return nil, MissingData }

	// Items as array
	items, err := itemsObj.Array()
	if err != nil { return nil, err }

	urls := make([]string, 0)

	// Enumerate
	for _, item := range items {
		// Find URL
		urlObj := item.Get(
			"gridVideoRenderer",
			"navigationEndpoint",
			"commandMetadata",
			"webCommandMetadata",
			"url",
		)
		if urlObj == nil { return nil, MissingData }

		// URL as string
		urlBytes, err := urlObj.StringBytes()
		if err != nil { return nil, err }
		url := string(urlBytes)

		if strings.HasPrefix(url, "/watch?v") {
			urls = append(urls, "https://www.youtube.com" + url)
		}
	}
	return urls, nil
}
