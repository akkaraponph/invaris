package evidence

import (
	"bytes"
	"reflect"
	"sort"
)

// Marker replaces redacted bytes.
const Marker = "[REDACTED]"

// Redact replaces every occurrence of each configured nonempty literal secret.
// Overlapping and adjacent matches are merged, so a secret cannot survive by
// straddling another one, and longer secrets are matched at each position
// before shorter ones. Only literal secrets are found: encoded, transformed or
// unconfigured secrets are not detected.
func Redact(data []byte, secrets []string) []byte {
	return redact(data, secrets, false)
}

// RedactTruncated additionally removes a trailing proper prefix of a secret,
// which a size limit may have cut in half.
func RedactTruncated(data []byte, secrets []string) []byte {
	return redact(data, secrets, true)
}

func redact(data []byte, secrets []string, truncated bool) []byte {
	secrets = usable(secrets)
	if len(secrets) == 0 {
		return append([]byte(nil), data...)
	}
	type span struct{ start, end int }
	var spans []span
	for _, secret := range secrets {
		for from := 0; from <= len(data)-len(secret); {
			i := bytes.Index(data[from:], []byte(secret))
			if i < 0 {
				break
			}
			spans = append(spans, span{from + i, from + i + len(secret)})
			from += i + 1
		}
		if truncated {
			for k := min(len(secret)-1, len(data)); k >= 1; k-- {
				if string(data[len(data)-k:]) == secret[:k] {
					spans = append(spans, span{len(data) - k, len(data)})
					break
				}
			}
		}
	}
	if len(spans) == 0 {
		return append([]byte(nil), data...)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var out []byte
	pos := 0
	for i := 0; i < len(spans); {
		start, end := spans[i].start, spans[i].end
		for i++; i < len(spans) && spans[i].start <= end; i++ {
			end = max(end, spans[i].end)
		}
		out = append(out, data[pos:start]...)
		out = append(out, Marker...)
		pos = end
	}
	return append(out, data[pos:]...)
}

func usable(secrets []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range secrets {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// RedactReport redacts every string in the report in place, before it is
// serialized or persisted. Excerpts of truncated observations also lose a
// secret prefix cut by the size limit.
func RedactReport(r *Report, secrets []string) {
	if len(usable(secrets)) == 0 {
		return
	}
	for i := range r.Observations {
		o := &r.Observations[i]
		if o.Truncated {
			o.Stdout = string(RedactTruncated([]byte(o.Stdout), secrets))
			o.Stderr = string(RedactTruncated([]byte(o.Stderr), secrets))
		}
	}
	redactValue(reflect.ValueOf(r).Elem(), secrets)
}

func redactValue(v reflect.Value, secrets []string) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(string(Redact([]byte(v.String()), secrets)))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			redactValue(v.Elem(), secrets)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			redactValue(v.Index(i), secrets)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			redactValue(v.Field(i), secrets)
		}
	}
}
