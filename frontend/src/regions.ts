/**
 * Administrative-division cascader (TR-04): province → city → district
 * selects backed by the generated table in regions.data.ts. Free-form text
 * used to allow "火星" as a province and silently broke local-first matching;
 * the cascader constrains input to real divisions while staying compatible
 * with older records that stored full-suffix or otherwise unknown values.
 *
 * Name convention mirrors backend domain.Region: province/city are SHORT
 * (浙江 / 杭州), districts keep their suffix (西湖区 / 义乌市).
 */
import { REGION_PROVINCES, REGION_CITIES, REGION_DISTRICTS } from './regions.data'

export const PROVINCES = REGION_PROVINCES

export function citiesOf(province: string): string[] {
  return REGION_CITIES[province] ?? []
}

export function districtsOf(province: string, city: string): string[] {
  return REGION_DISTRICTS[`${province}|${city}`] ?? []
}

/** Administrative suffixes accepted when matching legacy/user pasted text. */
const PROVINCE_SUFFIX = /(壮族自治区|回族自治区|维吾尔族|自治区|特别行政区|省|市)$/
const CITY_SUFFIX = /(自治州|地区|盟|市)$/

/**
 * Map a possibly full-suffix province string to its table short name.
 * Returns the input unchanged when nothing matches (unknown legacy value is
 * preserved, not destroyed).
 */
export function canonicalizeProvince(raw: string): string {
  const v = raw.trim()
  if (!v) return ''
  if (PROVINCES.includes(v)) return v
  const short = v.replace(PROVINCE_SUFFIX, '')
  return PROVINCES.includes(short) ? short : v
}

/** Same for a city within one province. */
export function canonicalizeCity(raw: string, province: string): string {
  const v = raw.trim()
  if (!v) return ''
  const list = citiesOf(province)
  if (list.includes(v)) return v
  const short = v.replace(CITY_SUFFIX, '')
  return list.includes(short) ? short : v
}

function earliestHit(text: string, candidates: string[]): { value: string; end: number } | null {
  let best: { value: string; start: number; end: number } | null = null
  for (const name of candidates) {
    const start = text.indexOf(name)
    if (start < 0) continue
    // Earliest occurrence wins; at the same start prefer the longer name.
    if (best === null || start < best.start || (start === best.start && name.length > best.value.length)) {
      best = { value: name, start, end: start + name.length }
    }
  }
  return best ? { value: best.value, end: best.end } : null
}

export interface RegionParts {
  province?: string
  city?: string
  district?: string
}

/**
 * Split a pasted full address line (企查查/天眼查 copy-paste style) into the
 * three cascader fields. Suffix-tolerant: both "浙江省杭州市西湖区…" and
 * "浙江杭州西湖…" resolve. Only the longest known divisions are consumed;
 * the street remainder is ignored (it belongs to the detailed-address field).
 * Direct municipalities map city to themselves (北京 → 北京).
 */
export function splitRegion(text: string): RegionParts {
  const out: RegionParts = {}
  if (!text) return out

  // Province: try each table name with an optional official suffix, take the
  // earliest occurrence (ties prefer the longer name).
  let provHit: { value: string; start: number; end: number } | null = null
  for (const name of PROVINCES) {
    const re = new RegExp(
      name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '(壮族自治区|回族自治区|维吾尔族自治区?|自治区|特别行政区|省|市)?',
    )
    const m = re.exec(text)
    if (m && (provHit === null || m.index < provHit.start || (m.index === provHit.start && name.length > provHit.value.length))) {
      provHit = { value: name, start: m.index, end: m.index + m[0].length }
    }
  }
  if (!provHit) return out
  out.province = provHit.value

  const rest1 = text.slice(provHit.end)
  const cityCandidates = citiesOf(provHit.value)
  // Direct municipality: its "city" is itself; district follows directly.
  const cityHit =
    provHit.value === cityCandidates[0] && cityCandidates.length === 1
      ? { value: provHit.value, end: 0 }
      : earliestHit(rest1, cityCandidates)
  if (!cityHit) return out
  out.city = cityHit.value

  const rest2 = (cityHit.end === 0 ? rest1 : rest1.slice(cityHit.end)).replace(/^市?/, '')
  // District matching is suffix-tolerant: pasted text often says 西湖 rather
  // than 西湖区. Match each division's core name with its trailing 区/县/旗/市
  // optional, take the earliest (longer core wins ties).
  let distHit: { value: string; end: number } | null = null
  for (const name of districtsOf(provHit.value, cityHit.value)) {
    const core = name.replace(/(自治县|自治旗|新区|区|县|旗|市)$/, '')
    const re = new RegExp(core.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '[区县旗市]?')
    const m = re.exec(rest2)
    if (m && (distHit === null || m.index < distHit.end - distHit.value.length)) {
      distHit = { value: name, end: m.index + m[0].length }
    }
  }
  if (distHit) out.district = distHit.value
  return out
}
