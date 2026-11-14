// Package discount is the subject of the Invaris Go example.
package discount

// Price returns cents reduced by percent. A percent of zero or less leaves the
// price unchanged and a percent of 100 or more makes it free.
func Price(cents, percent int) int {
	if percent <= 0 {
		return cents
	}
	if percent >= 100 {
		return 0
	}
	return cents - cents*percent/100
}
