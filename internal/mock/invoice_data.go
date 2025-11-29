package mock

import "github.com/shopspring/decimal"

// The invoice fixtures model documents produced by an extraction service:
// header fields, a customer block and line items. Amounts are computed
// with decimal arithmetic so the baseline is internally consistent to the
// cent; only the candidate drift (invoices.go) breaks that.

type lineItemRecord struct {
	SKU         string
	Description string
	Quantity    int64
	UnitPrice   decimal.Decimal
}

func (li lineItemRecord) amount() decimal.Decimal {
	return li.UnitPrice.Mul(decimal.NewFromInt(li.Quantity)).Round(2)
}

type customerRecord struct {
	Name  string
	Email string
	TaxID string
}

type invoiceRecord struct {
	ID        string
	Number    string
	Currency  string
	IssueDate string
	DueDate   string
	Customer  customerRecord
	Items     []lineItemRecord
	// TaxRate is a fraction such as 0.19.
	TaxRate decimal.Decimal
	// ExchangeRate converts Currency to USD.
	ExchangeRate decimal.Decimal
}

func (r invoiceRecord) subtotal() decimal.Decimal {
	sum := decimal.Zero
	for _, li := range r.Items {
		sum = sum.Add(li.amount())
	}
	return sum
}

// tax rounds half away from zero to cents, as most invoicing systems do.
func (r invoiceRecord) tax() decimal.Decimal {
	return r.subtotal().Mul(r.TaxRate).Round(2)
}

func (r invoiceRecord) total() decimal.Decimal {
	return r.subtotal().Add(r.tax())
}

func money(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// catalog returns the fixture invoices in list order. It builds a new
// slice on every call so callers cannot share or mutate fixture state.
func catalog() []invoiceRecord {
	return []invoiceRecord{
		{
			ID:        "inv-1001",
			Number:    "INV-2026-1001",
			Currency:  "EUR",
			IssueDate: "2026-02-03",
			DueDate:   "2026-03-05",
			Customer: customerRecord{
				Name:  "Nordlicht Logistik GmbH",
				Email: "accounts@nordlicht-logistik.example",
				TaxID: "DE811907980",
			},
			Items: []lineItemRecord{
				{SKU: "PAL-120", Description: "Euro pallet rental, per week", Quantity: 40, UnitPrice: money("3.75")},
				{SKU: "FRT-DE-07", Description: "Freight Hamburg to Munich", Quantity: 2, UnitPrice: money("412.50")},
				{SKU: "INS-STD", Description: "Cargo insurance, standard", Quantity: 1, UnitPrice: money("58.20")},
				{SKU: "HND-FEE", Description: "Handling fee", Quantity: 3, UnitPrice: money("19.99")},
			},
			TaxRate:      money("0.19"),
			ExchangeRate: money("1.0800"),
		},
		{
			ID:        "inv-1002",
			Number:    "INV-2026-1002",
			Currency:  "EUR",
			IssueDate: "2026-02-11",
			DueDate:   "2026-03-13",
			Customer: customerRecord{
				Name:  "Atelier Brun SARL",
				Email: "compta@atelier-brun.example",
				TaxID: "FR40303265045",
			},
			Items: []lineItemRecord{
				{SKU: "LIN-WHT-50", Description: "Linen fabric, white, 50 m roll", Quantity: 6, UnitPrice: money("189.00")},
				{SKU: "THR-SLK-12", Description: "Silk thread, assorted, box of 12", Quantity: 10, UnitPrice: money("24.35")},
				{SKU: "SHP-EXP", Description: "Express shipping", Quantity: 1, UnitPrice: money("35.00")},
			},
			TaxRate:      money("0.20"),
			ExchangeRate: money("1.0800"),
		},
		{
			ID:        "inv-1003",
			Number:    "INV-2026-1003",
			Currency:  "EUR",
			IssueDate: "2026-02-19",
			DueDate:   "2026-04-20",
			Customer: customerRecord{
				Name:  "Kaasboerderij de Vries B.V.",
				Email: "facturen@devries-kaas.example",
				TaxID: "NL004495445B01",
			},
			Items: []lineItemRecord{
				{SKU: "CLD-STO-1", Description: "Cold storage, per pallet day", Quantity: 120, UnitPrice: money("1.15")},
				{SKU: "TMP-LOG", Description: "Temperature logger rental", Quantity: 4, UnitPrice: money("12.40")},
			},
			TaxRate:      money("0.09"),
			ExchangeRate: money("1.0800"),
		},
	}
}

// findInvoice returns the fixture with the given ID.
func findInvoice(id string) (invoiceRecord, bool) {
	for _, r := range catalog() {
		if r.ID == id {
			return r, true
		}
	}
	return invoiceRecord{}, false
}
