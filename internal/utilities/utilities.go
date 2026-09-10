package utilities

import (
	"strconv"
	"strings"
)

// CompareIDs compares two stream IDs in the format "timestamp-sequence"
// Returns -1 if a < b, 1 if a > b, 0 if equal
func CompareIDs(a, b string) int {
	ap := strings.Split(a, "-")
	bp := strings.Split(b, "-")
	at, _ := strconv.ParseInt(ap[0], 10, 64)
	as, _ := strconv.ParseInt(ap[1], 10, 64)
	bt, _ := strconv.ParseInt(bp[0], 10, 64)
	bs, _ := strconv.ParseInt(bp[1], 10, 64)
	if at < bt {
		return -1
	} else if at > bt {
		return 1
	}
	if as < bs {
		return -1
	} else if as > bs {
		return 1
	}
	return 0
}
