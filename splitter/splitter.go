package splitter

import (
	"html"
	"strings"
)

func Split(text string) (chunks []string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		line = html.UnescapeString(line)
		chunks = append(chunks, line)
	}
	return chunks
}
