package architecture

// Analytics read-side sensors (ADR 0018, estate rule: a projector reads ONLY
// its own context's topic and never calls a sibling over REST or MCP).
//
//   - TestAnalyticsReadSideImportsNoOLTPOrSiblingClient: the files of the read
//     side (the two binaries, the reports adapter, the analytical store and the
//     projector's consumer and decoder) import nothing from the domain, the
//     application layer, the OLTP Postgres adapter, the outbox or the
//     delivery-promise client. The read side is derived from events, never
//     from the transactional core or a sibling's API.
//   - TestAnalyticsReadSideNamesOnlyItsOwnTopics: no string literal of those
//     files names a retailnetwork.* topic of another context.
//   - TestAnalyticsSensorBites proves both detectors flag what they must.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// analyticsRoots are the read side's non-test sources, relative to the repo
// root (this package sits two levels below it).
func analyticsFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, root := range []string{"cmd/co-projector", "cmd/co-reports", "internal/adapters/inbound/reportshttp",
		"internal/adapters/outbound/analyticsstore", "internal/analytics", "analytics"} {
		out = append(out, goFilesUnder(t, filepath.Join("..", "..", root), false)...)
	}
	matches, err := filepath.Glob(filepath.Join("..", "..", "internal", "adapters", "inbound", "kafka", "analytics_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if !strings.HasSuffix(m, "_test.go") {
			out = append(out, m)
		}
	}
	return out
}

// forbiddenAnalyticsImportSuffixes are the import-path tails the read side must
// never use.
func forbiddenAnalyticsImportSuffixes() []string {
	return []string{
		"/internal/domain", "/internal/application", "/internal/adapters/outbound/postgres", "/internal/adapters/outbound/outbox",
		"/internal/adapters/outbound/deliverypromise", "/internal/adapters/outbound/memory",
	}
}

// forbiddenAnalyticsImports lists "file:line imports path" for each import of
// a forbidden package (or below it).
func forbiddenAnalyticsImports(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		for _, suffix := range forbiddenAnalyticsImportSuffixes() {
			if strings.HasSuffix(path, suffix) || strings.Contains(path, suffix+"/") {
				out = append(out, filename+" imports "+path)
			}
		}
	}
	return out, nil
}

var topicLiteralRE = regexp.MustCompile(`"(retailnetwork\.[a-z0-9-]+\.[a-z0-9.-]+)"`)

// foreignTopics lists the retailnetwork.* topic literals of src that do not
// belong to the customer-ordering context.
func foreignTopics(src string) []string {
	var out []string
	for _, m := range topicLiteralRE.FindAllStringSubmatch(src, -1) {
		if !strings.HasPrefix(m[1], "retailnetwork.customer-ordering.") {
			out = append(out, m[1])
		}
	}
	return out
}

func TestAnalyticsReadSideImportsNoOLTPOrSiblingClient(t *testing.T) {
	files := analyticsFiles(t)
	if len(files) < 10 {
		t.Fatalf("the read side's sources were not found (%d files)", len(files))
	}
	for _, f := range files {
		found, err := forbiddenAnalyticsImports(f, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range found {
			t.Errorf("the analytics read side must not import the OLTP core or a sibling client: %s", v)
		}
	}
}

func TestAnalyticsReadSideNamesOnlyItsOwnTopics(t *testing.T) {
	sawOwn := false
	for _, f := range analyticsFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, topic := range foreignTopics(string(b)) {
			t.Errorf("%s names the topic %q: a projector reads only retailnetwork.customer-ordering.*", f, topic)
		}
		sawOwn = sawOwn || strings.Contains(string(b), `"retailnetwork.customer-ordering.events"`)
	}
	if !sawOwn {
		t.Error("no read-side file names retailnetwork.customer-ordering.events: the topic sensor is not looking at the consumer")
	}
}

func TestAnalyticsSensorBites(t *testing.T) {
	bad := "package x\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/claudioed/customer-ordering/internal/application/usecases\"\n\td \"github.com/claudioed/customer-ordering/internal/adapters/outbound/deliverypromise\"\n)\n"
	found, err := forbiddenAnalyticsImports("x.go", bad)
	if err != nil || len(found) != 2 {
		t.Fatalf("findings = %v, %v; want the application and the delivery-promise imports", found, err)
	}
	ok := "package x\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/claudioed/customer-ordering/internal/analytics/report\"\n\t\"github.com/claudioed/customer-ordering/internal/adapters/kafka/cloudevents\"\n)\n"
	if found, err := forbiddenAnalyticsImports("y.go", ok); err != nil || len(found) != 0 {
		t.Fatalf("a clean file was flagged: %v, %v", found, err)
	}
	if got := foreignTopics(`const a = "retailnetwork.order-sourcing.events"; const b = "retailnetwork.customer-ordering.events"; const c = "retailnetwork.customer-ordering.analytics.dlq"`); len(got) != 1 || got[0] != "retailnetwork.order-sourcing.events" {
		t.Fatalf("foreign topics = %v", got)
	}
}
