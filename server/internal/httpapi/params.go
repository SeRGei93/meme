package httpapi

import (
	"net/http"
	"strconv"
)

// parseLimitParam reads a bounded integer query parameter that defaults to def
// when omitted. A parameter that is present but empty is a client error, not a
// silent default. On failure it writes the contract's invalid_request error.
func parseLimitParam(w http.ResponseWriter, r *http.Request, name string, def, max int) (int, bool) {
	query := r.URL.Query()
	raw, present := query[name]
	if !present {
		return def, true
	}
	if len(raw) != 1 {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			name+" must be provided once", map[string]any{"field": name})
		return 0, false
	}

	parsed, err := strconv.Atoi(raw[0])
	if err != nil || parsed < 1 || parsed > max {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			name+" must be an integer between 1 and "+strconv.Itoa(max),
			map[string]any{"field": name})
		return 0, false
	}
	return parsed, true
}
