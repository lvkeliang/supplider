// Package redact masks personally-identifying / sensitive fields before a
// supplier archive is sent to a cloud LLM (AI 云端请求脱敏). A supplier
// record carries a contact phone, email, unified social credit code and
// address — none of which the model needs for a summary / compare / risk
// assessment — so we scrub them from the profile text first. It only guards
// DERIVED archive text; a user's own pasted requirement is left as-is (their
// call). Best-effort regex masking: never a hard guarantee, always better than
// sending raw contact data.
package redact

import "regexp"

var (
	// 11-digit mainland mobile: keep area head (1 + carrier digit) + last 4.
	mobileRe = regexp.MustCompile(`(1[3-9]\d)\d{4}(\d{4})`)
	// Email: source + local part are both opaque to the model.
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	// Unified social credit code / 身份证 (18 alnum or 18 digits): keep head+tail.
	code18Re = regexp.MustCompile(`([0-9A-Za-z]{4})[0-9A-Za-z]{10}([0-9A-Za-z]{4})`)
	// Any remaining 11+ digit run that looks like a phone/account no.
	longDigitsRe = regexp.MustCompile(`\d{11,}`)
)

const (
	mobileMask = `${1}****${2}`
	codeMask   = `${1}**********${2}`
)

// Text returns a copy of s with phone / email / long-identifier runs masked.
func Text(s string) string {
	s = mobileRe.ReplaceAllString(s, mobileMask)
	s = emailRe.ReplaceAllString(s, "***@***")
	s = code18Re.ReplaceAllString(s, codeMask)
	s = longDigitsRe.ReplaceAllString(s, "****")
	return s
}
