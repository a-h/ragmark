package site

import (
	"net/url"
	"os"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var englishCases = cases.Title(language.English)

func filePathToURL(fp string) (u string) {
	if fp == "." {
		return "/"
	}
	if fp == "index.md" {
		return "/"
	}
	list := strings.Split(fp, string(os.PathSeparator))

	fileName := list[len(list)-1]
	// If it's a markdown file, remove the extension.
	list[len(list)-1] = strings.TrimSuffix(fileName, ".md")
	// If it's an index file, remove the filename.
	if fileName == "index.md" {
		list = list[:len(list)-1]
	}

	// URL escape the paths.
	for i, v := range list {
		list[i] = url.PathEscape(v)
	}

	return "/" + strings.Join(list, "/")
}
