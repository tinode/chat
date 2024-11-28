package main

import (
	"encoding/json"
	"golang.org/x/net/html"
	"net/http"
	"strings"
	"time"
)

type LinkPreview struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	URL         string `json:"url"`
}

// PreviewLink handles the HTTP request, fetches the URL, and returns the link preview
func PreviewLink(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	client := &http.Client{
		Timeout: time.Second * 5,
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Non-OK HTTP status", http.StatusInternalServerError)
		return
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	metadata := extractMetadata(doc)

	linkPreview := LinkPreview{
		Title:       metadata["og:title"],
		Description: metadata["og:description"],
		Image:       metadata["og:image"],
		URL:         url,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(linkPreview); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func extractMetadata(n *html.Node) map[string]string {
	metaTags := map[string]string{}
	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			attrs := make(map[string]string)
			for _, attr := range n.Attr {
				attrs[attr.Key] = attr.Val
			}
			name := attrs["name"]
			property := attrs["property"]
			content := attrs["content"]

			if strings.HasPrefix(property, "og:") && content != "" {
				metaTags[property] = content
			} else if name != "" && content != "" {
				metaTags[name] = content
			}
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			traverse(child)
		}
	}

	traverse(n)

	if _, exists := metaTags["og:title"]; !exists {
		metaTags["og:title"] = extractTitle(n)
	}
	return metaTags
}

func extractTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		if n.FirstChild != nil {
			return n.FirstChild.Data
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		title := extractTitle(child)
		if title != "" {
			return title
		}
	}
	return ""
}
