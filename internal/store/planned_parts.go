package store

// Parts a preventive job uses, on its template: attrs.parts, a list of
// {sku, qty} (warehouse item codes). The forecast carries them onto every
// projected job, which is what the parts forecast adds up against stock; a
// job raised from the template is given them as its planned parts.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type PlannedPart struct {
	SKU string  `json:"sku"`
	Qty float64 `json:"qty"`
}

const maxPartQty = 100_000

// TemplateParts reads a template's parts, skipping anything malformed (a
// template written before the list was validated).
func TemplateParts(t PMTemplate) []PlannedPart {
	raw, _ := t.Attrs["parts"].([]any)
	out := []PlannedPart{}
	for _, item := range raw {
		m, _ := item.(map[string]any)
		sku, _ := m["sku"].(string)
		qty := numberAttr(m["qty"])
		if strings.TrimSpace(sku) == "" || qty == nil || *qty <= 0 {
			continue
		}
		out = append(out, PlannedPart{SKU: strings.TrimSpace(sku), Qty: *qty})
	}
	return out
}

// NormalizeTemplateParts checks attrs.parts when it is being written, and
// stores it in one shape: each part once, codes trimmed, quantities numbers.
// An empty list clears it.
func NormalizeTemplateParts(attrs map[string]any) error {
	raw, ok := attrs["parts"]
	if !ok {
		return nil
	}
	if raw == nil {
		attrs["parts"] = []any{}
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%w: parts is a list of {sku, qty}", ErrBadInput)
	}
	qty := map[string]float64{}
	order := []string{}
	for i, item := range list {
		m, _ := item.(map[string]any)
		sku, _ := m["sku"].(string)
		sku = strings.TrimSpace(sku)
		if sku == "" || len(sku) > 64 {
			return fmt.Errorf("%w: part %d needs a part code (sku) of 1–64 characters", ErrBadInput, i+1)
		}
		q := numberAttr(m["qty"])
		if q == nil || *q <= 0 || *q > maxPartQty {
			return fmt.Errorf("%w: part %s needs a quantity above 0 and at most %d", ErrBadInput, sku, maxPartQty)
		}
		key := strings.ToUpper(sku)
		if _, seen := qty[key]; !seen {
			order = append(order, sku)
		}
		qty[key] += *q
	}
	out := make([]any, 0, len(order))
	for _, sku := range order {
		out = append(out, map[string]any{"sku": sku, "qty": qty[strings.ToUpper(sku)]})
	}
	attrs["parts"] = out
	return nil
}

// partsLine is the parts as a work order's "parts required" text: "2 ×
// BRG-6205, 1 × BELT-A".
func partsLine(parts []PlannedPart) string {
	items := make([]string, 0, len(parts))
	for _, p := range parts {
		items = append(items, strconv.FormatFloat(p.Qty, 'f', -1, 64)+" × "+p.SKU)
	}
	sort.Strings(items)
	return strings.Join(items, ", ")
}
