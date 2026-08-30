package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Format is the output format selected by --output.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatWide  Format = "wide"
)

// ParseFormat parses the --output flag value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatTable, FormatJSON, FormatYAML, FormatWide:
		return Format(s), nil
	case "":
		return FormatTable, nil
	default:
		return "", fmt.Errorf("unknown output format %q; valid: table, json, yaml, wide", s)
	}
}

// Printer writes formatted output.
type Printer struct {
	Format Format
	Out    io.Writer
	Quiet  bool
}

// NewPrinter creates a Printer using the given format and writing to stdout.
func NewPrinter(format Format, quiet bool) *Printer {
	return &Printer{Format: format, Out: os.Stdout, Quiet: quiet}
}

// JSON encodes v as indented JSON and writes it to Out.
func (p *Printer) JSON(v interface{}) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// YAML encodes v as YAML and writes it to Out.
func (p *Printer) YAML(v interface{}) error {
	return yaml.NewEncoder(p.Out).Encode(v)
}

// Table writes a table with a header row and data rows.
// headers must have the same length as each row in rows.
func (p *Printer) Table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(p.Out, 0, 0, 3, ' ', 0)
	defer w.Flush()

	// Print header
	for i, h := range headers {
		if i > 0 {
			fmt.Fprint(w, "\t")
		}
		fmt.Fprint(w, h)
	}
	fmt.Fprintln(w)

	for _, row := range rows {
		for i, cell := range row {
			if i > 0 {
				fmt.Fprint(w, "\t")
			}
			fmt.Fprint(w, cell)
		}
		fmt.Fprintln(w)
	}
}

// Line writes a single line to Out.
func (p *Printer) Line(s string) {
	fmt.Fprintln(p.Out, s)
}

// LineJSON writes a single value as compact JSON on one line (for streaming output).
func (p *Printer) LineJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Fprintln(p.Out, string(data))
	return nil
}

// Confirm prints a prompt and reads y/n from stdin. Returns true on "y" or "yes".
// If yes is true (--yes flag), skips the prompt and returns true.
func Confirm(prompt string, yes bool) bool {
	if yes {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	var resp string
	_, _ = fmt.Scanln(&resp)
	return resp == "y" || resp == "yes"
}
