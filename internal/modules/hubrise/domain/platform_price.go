package domain

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
)

// PlatformPrice is the price list of a delivery platform: a HubRise variant whose prices are the
// published ones plus a markup. The platform's bridge is set to read this variant.
type PlatformPrice struct {
	VariantRef    string          `json:"variantRef"`
	Name          string          `json:"name"`
	MarkupPercent decimal.Decimal `json:"markupPercent"`
	// RoundToCents rounds the marked up price up to a multiple of this many cents (1 when empty).
	RoundToCents int64 `json:"roundToCents"`
}

// Apply returns price with the markup, rounded up. A free option stays free.
func (p PlatformPrice) Apply(price decimal.Decimal) decimal.Decimal {
	factor := decimal.NewFromInt(100).Add(p.MarkupPercent).Div(decimal.NewFromInt(100))
	step := decimal.New(max(p.RoundToCents, 1), -2)
	return price.Mul(factor).Div(step).Ceil().Mul(step)
}

// PlatformPrices is the JSONB column of the connection.
type PlatformPrices []PlatformPrice

// JSON is the value of the column; a nil list is an empty one.
func (p PlatformPrices) JSON() string {
	if p == nil {
		return "[]"
	}
	data, _ := json.Marshal(p) // Strings and numbers: it cannot fail.
	return string(data)
}

// Scan reads the column.
func (p *PlatformPrices) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*p = nil
		return nil
	case []byte:
		return json.Unmarshal(v, p)
	case string:
		return json.Unmarshal([]byte(v), p)
	default:
		return fmt.Errorf("hubrise: platform prices from %T", src)
	}
}
