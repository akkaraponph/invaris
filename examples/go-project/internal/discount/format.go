package discount

import "strconv"

// Label renders a price for display.
func Label(cents int) string {
	return strconv.Itoa(cents/100) + "." + strconv.Itoa(cents%100/10) + strconv.Itoa(cents%10)
}
