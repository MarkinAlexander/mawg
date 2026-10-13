// ЗЕРКАЛО бэкенд-JSON (44-дока, Фаза 1): только поля, которыми панель
// реально оперирует. Бэкенд - internal/web/server.go и соседние файлы;
// при изменении payload'а на бэкенде правится и этот файл.
// Даты приходят строками (time.Time JSON) - парсинг на стороне UI.

// --- MagiTrickle: группы/правила ---

export interface Rule {
  id: string
  type: string // domain | namespace | wildcard | regex | subnet | subnet6
  rule: string
  name?: string
  enable: boolean
}

// политика «кто ходит в группу» + поведение при отвале
export interface GroupPolicy {
  onDead?: '' | 'direct' | 'iface'
  iface?: string
  sourcesMode?: '' | 'all' | 'all+router' | 'list'
  sources?: string[]
  sourcesRouter?: boolean
}

export interface Group {
  id: string
  name: string
  color?: string
  interface: string
  enable: boolean
  sources?: string[]
  rules?: Rule[]
}

export interface GroupView extends Group {
  policy?: GroupPolicy | null
  cascade?: boolean
  degraded?: string // '' | 'direct' | 'direct-pending' | 'iface' | 'iface:<...>'
  interfaceTitle?: string
}

export interface MtGroupsRes {
  available: boolean
  groups: GroupView[]
  glitchy: boolean // глючная сборка MT: массовые записи усекают группы
  wrapper: boolean // в OUTPUT видна обёртка юзера (MR_*)
  sources: boolean // MT понимает sources (форк rev3+)
}

export interface DuplicatesRes {
  duplicates: Record<string, string[]>
}

export interface MtIface {
  device: string
  title?: string
}

export interface PresetRule {
  type: string
  rule: string
  enable?: boolean
}

export interface Preset {
  id: string
  title: string
  source?: string
  rules: PresetRule[]
}

export interface PresetsRes {
  presets: Preset[]
  sourceUrl: string
  sourceNote: string
}

export interface CascadeInfo {
  group: string
  name?: string
  source: string // "pool:x" | "iface:wg0"
  via: string
  hosts: string[]
}

export interface CascadesRes {
  available: boolean
  cascades?: CascadeInfo[]
  wrapper?: boolean
}

export interface PolicyCycles {
  fail: number
  restore: number
}

// --- бэкапы MT ---

export interface BackupInfo {
  name: string
  kind: 'shadow' | 'config'
  size: number
  modified: string
}

// --- настройки сервера ---

export interface IPAllow {
  v: string
  on: boolean
}

export interface ServerSettings {
  listenAddr: string
  port: number
  allowedIps: IPAllow[]
  authDisabled: boolean
  authEnabled: boolean
  hasCreds: boolean
  clientIp: string
  runningAddr?: string
  runningPort?: number
}

// --- статус/пулы ---

export interface PoolSettings {
  platform?: string
  engineMode?: string // "singbox" - пул через tun-движок
  tunName?: string
  source?: string
  probeHost?: string
  probeType?: string
  checkIntervalSec?: number
  failThreshold?: number
  cooldownMin?: number
  fallback?: string
  keepalive?: number
  maxRttMs?: number
  keeneticSlot?: string
  openwrtProto?: string
  magitrickleGroupID?: string
  engineDetour?: string
  updateInt?: number
}

export interface ConfigView {
  file: string
  original?: string
  endpoint?: string
  enabled: boolean
  coolForSec?: number
  conflict?: string
}

export interface PremiumCountry {
  code: string
  name: string
}

export interface PremiumView {
  imported: boolean
  country?: string
  countries: PremiumCountry[]
  pending?: boolean
}

export interface ProtoBadge {
  label: string
  kind: string // awg3 | awg | wg | proxy
  count: number
}

export interface PoolView {
  name: string
  platform: string
  device: string
  slot?: string
  mode: string // up | down | ...
  activeFile: string
  activeEndpoint: string
  lastResult: string
  lastError: string
  rotations: number
  disabled: boolean
  consecFails: number
  refreshFails?: number
  lastRefreshErr?: string
  subRefreshAt?: string
  settings: PoolSettings
  configs: ConfigView[]
  premium: PremiumView
  protos: ProtoBadge[]
  free?: boolean
}

export interface Status {
  version: string
  platform: string
  backendName: string
  authEnabled: boolean
  nativeAWG3: boolean
  mtSources: boolean
  pools: PoolView[]
  engine?: EngineInfo
}

// GET /magitrickle: шапке нужен только available (легаси MTState);
// groups читает карточка групп, здесь зеркалим как неизвестное
export interface MagitrickleRes {
  available: boolean
  error?: string
  groups?: unknown[]
}

// --- интерфейсы/слоты/wan-probe ---

// проба здоровья (store.ProbeConfig): цель - публичный IPv4 или http(s) URL;
// PUT с target:'' = убрать проверку (бэкенд превращает в nil)
export interface ProbeConfig {
  type: string // http | icmp
  target: string
  maxRttMs?: number
}

export interface IfaceInfo {
  device: string
  slot?: string
  description?: string
  mode: string // managed | external | hidden
  pool?: string
  linkUp: boolean
  connected: boolean
  handshakeAgo: number
  probe?: ProbeConfig | null
  probeStatus?: string
}

export interface WanProbeInfo {
  enabled: boolean
  probe?: ProbeConfig | null
}

// --- события/система ---

export interface AppEvent {
  time: string
  pool: string
  kind: string
  message: string
}

export interface SystemItem {
  id: string
  title: string
  installed: boolean
  version: string
  action?: string
  actionLabel?: string
  flavors?: string[]
  confirm?: string
  requiresReboot?: boolean
  note?: string
  statusText?: string
  // для замены ядра: сколько займёт копия старого и сколько свободно
  backupBytes?: number
  freeBytes?: number
}

// STATUS.engine: движок sing-box (зеркало internal/web/server.go)
export interface TunInfo {
  pool?: string
  name?: string
  nodes?: number
  disabled?: boolean
  probePort?: number
  [key: string]: unknown
}

export interface EngineInfo {
  available?: boolean
  running?: boolean
  version?: string
  mode?: string
  fragment?: string
  cacheFile?: string
  tuns?: TunInfo[]
  [key: string]: unknown
}

// POST /system/install: вывод установки (легаси showResult)
export interface InstallRes {
  ok: boolean
  output?: string
  error?: string
}

export interface SystemCheckRes {
  platform: string
  items: SystemItem[]
}

export interface UpdateInfo {
  current: string
  latest: string
  update: boolean
  url?: string
  error?: string
}

export interface LanClient {
  ip: string
  mac?: string
  name?: string
}

// --- пулы: создание/действия ---

export interface Captcha {
  id: string
  image: string
  hint?: string
}

export interface SourcePlan {
  pool?: string
  premium?: boolean
  tun?: string
  applied?: number
  added?: number
  probeOk?: boolean
  probeMs?: number
  duplicates?: string[]
  skipped?: string[]
  warnings?: string[]
  captchaRequired?: boolean
  captcha?: Captcha
}

// --- наборы интерфейсов (ротация) ---

export interface BundleView {
  name: string
  members: string[]
  groups: string[]
  currentMember?: string
  memberDevices: Record<string, string>
}
