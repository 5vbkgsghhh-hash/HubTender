package calc

import (
	"fmt"
	"math"
)

// CalculateLinkedMaterialQuantity is the VOR recipe: quantity of the work,
// multiplied by the unit conversion and the stored material consumption.
// The resulting quantity already includes consumption; amount calculation
// must not multiply it a second time for a linked material.
func CalculateLinkedMaterialQuantity(workQuantity float64, conversion, consumption *float64) (float64, error) {
	coefficient := func(p *float64) float64 {
		if p == nil {
			return 1
		}
		return *p
	}
	conv, cons := coefficient(conversion), coefficient(consumption)
	positive := func(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
	quantity := workQuantity * conv * cons
	if !positive(workQuantity) || !positive(conv) || !positive(cons) || !positive(quantity) {
		return 0, fmt.Errorf("linked material requires positive finite work quantity and coefficients")
	}
	return quantity, nil
}
