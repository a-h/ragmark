package prompts

import (
	"embed"
	"fmt"
	"io"
	"strings"

	"github.com/a-h/ragmark/db"
)

func Chat(context []db.Chunk, msg string) string {
	var sb strings.Builder
	sb.WriteString("Use the following pieces of context to answer the question at the end. If you don't know the answer, just say that you don't know, don't try to make up an answer.\n")

	for _, doc := range context {
		sb.WriteString(fmt.Sprintf("Context from %s:\n%s\n\n", doc.Path, doc.Text))
	}
	sb.WriteString("Question: ")
	sb.WriteString(msg)
	sb.WriteString("\nSuccint Answer: ")
	return sb.String()
}

func Summarise(content string) string {
	var sb strings.Builder
	sb.WriteString("Summarise the following markdown document. Include main keywords.\n")
	sb.WriteString(content)
	return sb.String()
}

//go:embed rdf
var rdfFS embed.FS

func ExtractRelationships(subject, content string) (s string, err error) {
	var sb strings.Builder
	sb.WriteString("Use the provided hierarchy of known RDF predicates to extract subject, predicate and objects from the markdown document. Quote the subject and objects.\n")
	sb.WriteString("Examples:\n")
	sb.WriteString("  \"john\" ies:trusts \"sally\"\n")
	sb.WriteString("  \"London\" ies:nearTo \"Oxford\"\n")
	sb.WriteString("  \"BAE Systems\" ies:make \"F35-II\"\n")
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Use the subject %q.\n", subject))
	sb.WriteString("\n")
	sb.WriteString("Here is a tree of known RDF predicates:\n\n")
	f, err := rdfFS.Open("./ies_relationships.rdf")
	if err != nil {
		return s, fmt.Errorf("failed to open relationships file: %w", err)
	}
	defer f.Close()
	if _, err = io.Copy(&sb, f); err != nil {
		return s, fmt.Errorf("failed to copy relationships file: %w", err)
	}
	sb.WriteString("Here is the markdown document:\n\n")
	sb.WriteString(content)
	sb.WriteString("\n\n")
	sb.WriteString("Here is the list of extracted relationships:")
	return sb.String(), nil
}

func ExtractType(content string) (s string, err error) {
	var sb strings.Builder
	sb.WriteString("Use the provided hierarchy of known RDF predicates to categorise a given markdown document into categories, based on its subject.\n")
	sb.WriteString("The document's subject may fall into a number of categories.\n")
	sb.WriteString("Example outputs:\n")
	sb.WriteString("  \"UK\" rdf:type ies:Country\n")
	sb.WriteString("  \"Heathrow Airport\" rdf:type ies:Airport\n")
	sb.WriteString("  \"Islam\" rdf:type ies:Religion\n")
	sb.WriteString("  \"GBP\" rdf:type ies:Currency\n")
	sb.WriteString("  \"Apple iPhone\" rdf:type: ies:MobileHandset\n")
	sb.WriteString("\n")
	sb.WriteString("Here is a tree of known RDF predicates:\n\n")
	f, err := rdfFS.Open("rdf/ies_types.rdf")
	if err != nil {
		return s, fmt.Errorf("failed to open types file: %w", err)
	}
	defer f.Close()
	if _, err = io.Copy(&sb, f); err != nil {
		return s, fmt.Errorf("failed to copy types file: %w", err)
	}
	sb.WriteString("Here is the markdown document:\n\n")
	sb.WriteString(content)
	sb.WriteString("\n\n")
	sb.WriteString("Outputs:\n")
	return sb.String(), nil
}
