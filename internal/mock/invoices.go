package mock

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"
)

// Candidate drift. The candidate invoice differs from the baseline in
// exactly these ways:
//
//   - line_items are returned in reverse order, so positional comparison
//     reports every item as changed; keyed comparison on "sku" does not.
//   - exchange_rate is 1.0800004 instead of 1.0800, a difference inside a
//     small numeric tolerance.
//   - total is increased by Config.InvoiceTotalDelta, so subtotal + tax no
//     longer equals total and extraction rules fail.
//   - customer.email is renamed to customer.email_address, so a schema that
//     requires customer.email fails and comparison reports one removed and
//     one added field.
//
// request_id and generated_at differ on every response in both variants.

// candidateRateOffset is added to the exchange rate in the candidate
// (4e-7, inside an absolute tolerance of 0.00001).
func candidateRateOffset() decimal.Decimal { return decimal.New(4, -7) }

// Money and rates are encoded as json.Number built from fixed-point
// decimal strings, so the JSON text contains exactly "1234.50" and never a
// float64 rendering.
type lineItemJSON struct {
	SKU         string      `json:"sku"`
	Description string      `json:"description"`
	Quantity    json.Number `json:"quantity"`
	UnitPrice   json.Number `json:"unit_price"`
	Amount      json.Number `json:"amount"`
}

type customerJSON struct {
	Name         string `json:"name"`
	Email        string `json:"email,omitempty"`
	EmailAddress string `json:"email_address,omitempty"`
	TaxID        string `json:"tax_id"`
}

type invoiceJSON struct {
	ID            string         `json:"id"`
	InvoiceNumber string         `json:"invoice_number"`
	Currency      string         `json:"currency"`
	IssueDate     string         `json:"issue_date"`
	DueDate       string         `json:"due_date"`
	Customer      customerJSON   `json:"customer"`
	LineItems     []lineItemJSON `json:"line_items"`
	Subtotal      json.Number    `json:"subtotal"`
	TaxRate       json.Number    `json:"tax_rate"`
	Tax           json.Number    `json:"tax"`
	Total         json.Number    `json:"total"`
	ExchangeRate  json.Number    `json:"exchange_rate"`
	RequestID     string         `json:"request_id"`
	GeneratedAt   string         `json:"generated_at"`
}

type invoiceSummaryJSON struct {
	ID            string `json:"id"`
	InvoiceNumber string `json:"invoice_number"`
	CustomerName  string `json:"customer_name"`
	IssueDate     string `json:"issue_date"`
	Currency      string `json:"currency"`
}

type invoiceListJSON struct {
	Invoices []invoiceSummaryJSON `json:"invoices"`
	Count    int                  `json:"count"`
}

func cents(d decimal.Decimal) json.Number { return json.Number(d.StringFixed(2)) }

// renderInvoice builds the response document for rec as served by v.
func (s *Server) renderInvoice(rec invoiceRecord, v Variant) invoiceJSON {
	items := make([]lineItemJSON, len(rec.Items))
	for i, li := range rec.Items {
		items[i] = lineItemJSON{
			SKU:         li.SKU,
			Description: li.Description,
			Quantity:    json.Number(strconv.FormatInt(li.Quantity, 10)),
			UnitPrice:   cents(li.UnitPrice),
			Amount:      cents(li.amount()),
		}
	}
	out := invoiceJSON{
		ID:            rec.ID,
		InvoiceNumber: rec.Number,
		Currency:      rec.Currency,
		IssueDate:     rec.IssueDate,
		DueDate:       rec.DueDate,
		Customer:      customerJSON{Name: rec.Customer.Name, Email: rec.Customer.Email, TaxID: rec.Customer.TaxID},
		LineItems:     items,
		Subtotal:      cents(rec.subtotal()),
		TaxRate:       json.Number(rec.TaxRate.StringFixed(2)),
		Tax:           cents(rec.tax()),
		Total:         cents(rec.total()),
		ExchangeRate:  json.Number(rec.ExchangeRate.StringFixed(4)),
		RequestID:     newRequestID(),
		GeneratedAt:   s.timestamp(),
	}
	if v == Candidate {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		out.ExchangeRate = json.Number(rec.ExchangeRate.Add(candidateRateOffset()).StringFixed(7))
		out.Total = cents(rec.total().Add(s.delta))
		out.Customer.EmailAddress, out.Customer.Email = out.Customer.Email, ""
	}
	return out
}

// handleInvoice serves GET /{v}/invoices/{id}.
func (s *Server) handleInvoice(v Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, ok := findInvoice(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		writeJSON(w, http.StatusOK, s.renderInvoice(rec, v))
	}
}

// handleInvoiceList serves GET /{v}/invoices. The list is identical in
// both variants; suites capture an ID from it for later cases.
func (s *Server) handleInvoiceList(Variant) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		recs := catalog()
		out := invoiceListJSON{Invoices: make([]invoiceSummaryJSON, len(recs)), Count: len(recs)}
		for i, rec := range recs {
			out.Invoices[i] = invoiceSummaryJSON{
				ID:            rec.ID,
				InvoiceNumber: rec.Number,
				CustomerName:  rec.Customer.Name,
				IssueDate:     rec.IssueDate,
				Currency:      rec.Currency,
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
