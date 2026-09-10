/**
 * Establishment-date parsing (TR-06). A native <input type="date"> only
 * accepts ISO and silently rejects pasting the formats enterprise-lookup
 * sites (企查查/天眼查) actually use:
 *   2005年3月15日 / 2005/3/15 / 2005.3.15 / 2005-03-15 00:00:00.0
 * The form now uses a text field with onPaste/onBlur normalization; every
 * accepted shape is canonicalised to the ISO YYYY-MM-DD the backend stores
 * (risk-rule R201 parses ISO). Pure so the matrix is unit-tested.
 */

const ISO_DATE = /^(\d{4})[-/.年](\d{1,2})[-/.月](\d{1,2})[日号]?$/

function daysInMonth(year: number, month: number): number {
  return new Date(year, month, 0).getDate() // month is 1-based here
}

function buildISO(year: number, month: number, day: number): string | null {
  if (year < 1900 || year > 2100) return null
  if (month < 1 || month > 12) return null
  if (day < 1 || day > daysInMonth(year, month)) return null
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${year}-${pad(month)}-${pad(day)}`
}

/**
 * Normalize one date string to ISO. Returns '' for blank input (date not
 * provided) and null for unparseable/invalid values (caller shows an error).
 */
export function normalizeEstablishmentDate(raw: string): string | null {
  const s = raw.trim()
  if (!s) return ''

  // 天眼查/API datetimes: "2005-03-15 00:00:00", "2005-03-15 00:00:00.0",
  // RFC3339 "2005-03-15T00:00:00Z" — take the date prefix.
  const dt = /^(\d{4}-\d{1,2}-\d{1,2})[T\s]/.exec(s)
  if (dt) {
    const [y, m, d] = dt[1].split('-').map(Number)
    return buildISO(y, m, d)
  }

  // Compact 8-digit form 20050315.
  if (/^\d{8}$/.test(s)) {
    return buildISO(Number(s.slice(0, 4)), Number(s.slice(4, 6)), Number(s.slice(6, 8)))
  }

  // ISO / slash / dot / Chinese layouts (padded or not).
  const m = ISO_DATE.exec(s)
  if (m) {
    return buildISO(Number(m[1]), Number(m[2]), Number(m[3]))
  }

  return null
}
