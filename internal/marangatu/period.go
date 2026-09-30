package marangatu

import (
	"fmt"
	"time"
)

// Formatos de período: un mes (registro mensual, obligación 955) o un año
// (registro anual, obligación 956, por ejemplo IRP-RSP anual).
const (
	monthLayout = "2006-01"
	yearLayout  = "2006"
)

// Period es el período fiscal del archivo.
type Period struct {
	Start  time.Time
	Annual bool
}

// ParsePeriod lee "AAAA-MM" (mensual) o "AAAA" (anual).
func ParsePeriod(p string) (Period, error) {
	if len(p) == len(yearLayout) {
		if year, err := time.Parse(yearLayout, p); err == nil {
			return Period{Start: year, Annual: true}, nil
		}
	}
	if month, err := time.Parse(monthLayout, p); err == nil && len(p) == len(monthLayout) {
		return Period{Start: month}, nil
	}
	return Period{}, fmt.Errorf("%w: %q", ErrInvalidPeriod, p)
}

// fileToken es el período como va en el nombre del archivo: MMAAAA o AAAA.
func (p Period) fileToken() string {
	if p.Annual {
		return p.Start.Format(yearLayout)
	}
	return p.Start.Format("012006")
}
