package reportshttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// Wire bodies (snake_case, apis/openapi.yaml tag `reports`).

type noticeKindBody struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type orderingDayBody struct {
	Day                    string           `json:"day"`
	OrdersPlaced           int              `json:"orders_placed"`
	OrdersCancelled        int              `json:"orders_cancelled"`
	ConsolidatedTierOrders int              `json:"consolidated_tier_orders"`
	PickupPointOrders      int              `json:"pickup_point_orders"`
	SplitShipmentOrders    int              `json:"split_shipment_orders"`
	SplitShipmentShare     *float64         `json:"split_shipment_share"`
	ShopperNoticesDecided  int              `json:"shopper_notices_decided"`
	ShopperNoticesByKind   []noticeKindBody `json:"shopper_notices_by_kind"`
	ShopperNoticesOther    int              `json:"shopper_notices_other"`
}

type orderingReportBody struct {
	From      string            `json:"from"`
	To        string            `json:"to"`
	Days      []orderingDayBody `json:"days"`
	Freshness freshnessBody     `json:"freshness"`
}

func orderingBody(rg report.Range, rows []report.OrderingRow, f report.Freshness) orderingReportBody {
	out := orderingReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]orderingDayBody, 0, len(rows)), Freshness: freshnessOf(f)}
	for _, r := range rows {
		kinds := make([]noticeKindBody, 0, len(r.NoticesByKind))
		for _, k := range r.NoticesByKind {
			kinds = append(kinds, noticeKindBody{Kind: k.Kind, Count: k.Count})
		}
		out.Days = append(out.Days, orderingDayBody{
			Day: report.FormatDay(r.Day), OrdersPlaced: r.OrdersPlaced, OrdersCancelled: r.OrdersCancelled,
			ConsolidatedTierOrders: r.ConsolidatedTierOrders, PickupPointOrders: r.PickupPointOrders,
			SplitShipmentOrders: r.SplitShipmentOrders, SplitShipmentShare: r.SplitShipmentShare,
			ShopperNoticesDecided: r.NoticesDecided, ShopperNoticesByKind: kinds, ShopperNoticesOther: r.NoticesOther,
		})
	}
	return out
}

type freshnessBody struct {
	AsOf       *time.Time `json:"as_of"`
	LagSeconds *float64   `json:"lag_seconds"`
}

func freshnessOf(f report.Freshness) freshnessBody {
	return freshnessBody{AsOf: f.AsOf, LagSeconds: f.LagSeconds}
}

// problem is one RFC 9457 problem type: its status, slug and title.
type problem struct {
	status int
	slug   string
	title  string
}

var (
	invalidRange          = problem{http.StatusBadRequest, "invalid-report-range", "The report range is invalid"}
	notFound              = problem{http.StatusNotFound, "not-found", "Resource not found"}
	methodNotAllowed      = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable"}
	internalError         = problem{http.StatusInternalServerError, "internal-error", "Internal error"}
)

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemBody{Type: p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath()})
}

// logSafe strips line breaks so a logged value cannot forge a log line.
var logSafe = strings.NewReplacer("\n", " ", "\r", " ")

// writeInternal logs err and answers a 500 whose detail never leaks it.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "report query failed", "error", logSafe.Replace(err.Error()), "path", logSafe.Replace(r.URL.Path))
	writeProblem(w, r, internalError, "an unexpected error occurred")
}
