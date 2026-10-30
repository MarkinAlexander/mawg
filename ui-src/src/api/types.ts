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

// служебная группа-каскад (store.CascadeEntry); приходит внутри GroupView
export interface CascadeEntry {
  group: string
  name?: string
  source: string // "pool:x" | "iface:wg0"
  via: string
  domains?: string[]
}

export interface GroupView extends Group {
  policy?: GroupPolicy | null
  cascade?: CascadeEntry | null
  // служебные группы DNS (путь зоны / туннель общего DNS-каскада);
  // управляются вкладкой DNS, вручную редактировать бессмысленно
  dnsZone?: boolean
  dnsTunnel?: boolean
  degraded?: string // '' | 'direct' | 'direct-pending' | 'iface' | 'iface:<...>'
  interfaceTitle?: string
}

// запись реестра «пул ↔ устройство через пул» (97-док): зеркало
// MT-группы устройства; ID группы не хранится - связь по имени
export interface DeviceBinding {
  pool: string
  ips: string[]
  name: string
  created?: string
}

export interface MtGroupsRes {
  available: boolean
  groups: GroupView[]
  glitchy: boolean // глючная сборка MT: массовые записи усекают группы
  wrapper: boolean // в OUTPUT видна обёртка юзера (MR_*)
  sources: boolean // MT понимает sources (форк rev3+)
}

// ссылка на вхождение паттерна (GET /mt/duplicates): где ещё живёт правило
export interface DupInfo {
  groupId?: string
  groupName?: string
  ruleId?: string
  ruleType?: string
}

export interface DuplicatesRes {
  duplicates: Record<string, DupInfo[]>
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
  note?: string // предупреждение о составе (общие подсети Meta и т.п.)
  rules: PresetRule[]
}

export interface PresetsRes {
  presets: Preset[]
  sourceUrl: string
  sourceNote: string
}

// ответ mtImportRules (server.go): импорт правил текстом
export interface ImportRulesRes {
  added: number
  skipped: number
  bad?: string[]
}

// GET /cascades (cascade.Info = CascadeEntry + статусы хука)
export interface CascadeInfo extends CascadeEntry {
  enabled: boolean
  hook: boolean
  rules: number
  err?: string
}

export interface CascadesRes {
  available: boolean
  cascades?: CascadeInfo[]
  wrapper?: boolean
}

// гистерезис политик (server.go getPolicyCycles): {failCycles, restoreCycles}
export interface PolicyCycles {
  failCycles: number
  restoreCycles: number
}

// --- бэкапы MT ---

export interface BackupInfo {
  name: string
  kind: 'shadow' | 'config' | 'full'
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
  engineCore?: string // "mihomo" - пул крутит второе ядро (88-док); пусто = sing-box
  groupMode?: string // движковый пул: "selector" - ручной выбор узла; "rotate" - ротация по таймеру; "urltest" (дефолт) - лучший пинг
  rotateSec?: number // интервал ротации для groupMode="rotate", сек
  nodeOff?: Record<string, boolean> // выключенные узлы подписки (имя -> true)
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
  updateIntervalH?: number
  // источник Amnezia: доступные локации у gateway (легаси updateSpAmneziaCountry)
  amnezia?: {
    country?: string
    countries?: { code: string; name?: string; protocols?: string[] }[]
  }
  // генератор WARP (77-док): параметры пула - кнопка «Генерация нового
  // конфига» в карточке добавляет конфиг с теми же параметрами
  warp?: {
    mode: string // random | fixed
    endpoint?: string
    sites?: string[]
    subnets?: string[] // разрешённые подсети (AllowedIPs), не домены
  }
  // умный каскад (86-док): при блокировке пула трафик заворачивается
  // через первый живой пул via, с возвратом по интервалу/расписанию/кнопке
  smartCascade?: SmartCascadeSettings
  // исключения пула (94-док): адреса, которые этот пул не обрабатывает;
  // общий список на все пулы - GET/PUT /excludes (страница правил)
  excludes?: string[]
}

export interface SmartCascadeSettings {
  via?: string[] // пулы запасного пути в порядке приоритета
  // когда включать: fails - после triggerFails отказов подряд активного
  // конфига (тот же конфиг сразу пробуется через Via); exhaust (пусто) -
  // после перебора всех конфигов пула
  triggerMode?: string
  triggerFails?: number // для fails, 0 = 3
  return?: {
    mode?: string // interval | schedule | manual (пусто = schedule 03:00)
    intervalH?: number // для interval, 0 = 6
    timeOfDay?: string[] // для schedule, "HH:MM"
  }
  minDwellMin?: number // антифлап: минут в состоянии до авто-возврата, 0 = 10
}

// зеркало автомата умного каскада (PoolView.smart, /status)
export interface SmartCascadeState {
  on?: boolean
  via?: string // активный запасной пул
  group?: string // id служебной группы каскада
  since?: string
  nextCheck?: string
  lastDetail?: string
  ensureErr?: string // служебная группа не создалась (MT недоступен?)
}

export interface ConfigView {
  file: string
  original?: string
  endpoint?: string
  enabled: boolean
  coolForSec?: number
  conflict?: string
  awg?: string // plain | awg2 | awg3 - какие кнопки скачивания показывать
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
  // структурированный итог той же пробы (время - unix-сек)
  probeOk?: boolean | null
  probeMs?: number
  probeAt?: number
  rotations: number
  disabled: boolean
  consecFails: number
  refreshFails?: number
  lastRefreshErr?: string
  subRefreshAt?: string
  smart?: SmartCascadeState | null
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
  // функционал исключений выключен (95-док, слово владельца 30.10):
  // UI гасит блоки исключений плашкой и disabled-кнопками
  excludesDisabled?: boolean
  pools: PoolView[]
  engine?: EngineInfo
  // ядра движков (88-док): singbox (копия engine) и mihomo;
  // available=false - ядро не установлено
  engines?: Record<string, EngineInfo>
}

// GET /magitrickle: шапке нужен только available (легаси MTState);
// groups читает карточка групп, здесь зеркалим как неизвестное
export interface MagitrickleRes {
  available: boolean
  error?: string
  groups?: unknown[]
}

// GET /dns/probe: проба одного DNS-уровня канарейкой (63-док).
// verdict: clean | spoof | noanswer | error
// ip - нога сервиса, которая ответила (перебор Level.IPs, Фаза E)
export interface DnsProbeRow {
  level: string
  kind: 'udp' | 'dot' | 'doh' | string
  server: string
  verdict: 'clean' | 'spoof' | 'noanswer' | 'error' | string
  canonical: number
  total: number
  ms: number
  ip?: string
  detail?: string
}

// свой сервер карточки «Проверка DNS и сети» (GET/PUT /dns/probe/servers):
// kind udp - обычный DNS (host:port), dot - tls://host[:853],
// doh - https://host/dns-query
export interface DnsProbeServer {
  name: string
  kind: 'udp' | 'dot' | 'doh' | string
  server: string
}

// GET /dns/keenetic/own: собственные DNS-серверы Keenetic из
// running-config (только просмотр, 71-док; вердикты - GET /dns/probe)
export interface KeeneticOwn {
  doh: { uri: string; format?: string }[]
  dot: { address: string; port?: string; sni?: string }[]
  udp: { interface: string; servers: string[] }[]
  v6?: { interface: string; servers: string[] }[]
}

// GET /dns/state: состояние DNS-стека роутера (46-док §3).
// platform: "openwrt" - полный разрез (dnsmasq/кэш/перенаправление);
// "keenetic" - только перехват MT_DNSOR + апстримы MT (71-док: свои
// серверы Keenetic панель не трогает), dnsmasq нулевой.
export interface DnsState {
  platform: 'openwrt' | 'keenetic'
  dnsmasq: {
    servers: string[]
    inMT: boolean
    noresolv: boolean
    cacheSize?: number
    strict: boolean
  }
  mt: {
    upstream: string
    upstreams?: string[]
    remap53: boolean
  }
  // собственные IPv4 роутера (петлезащита upstream: адрес dnsmasq
  // на собственном IP при dnsmasq.inMT замыкает петлю)
  self?: string[]
}

// GET /dns/upstreams: живой стейт каскада апстримов dns-proxy MT
// (форк rev8+, 64-док §6). capable=false - MT старой ревизии: статус и
// редактор недоступны, апстримы видны в /dns/state из config.yaml.
export interface DnsUpstream {
  address: string
  alive: boolean
  active: boolean
  fails: number
  oks: number
  lastSwap: string
  reason?: string
}

export interface DnsUpstreamsState {
  available: boolean
  health: {
    enabled: boolean
    interval: string
    timeout: string
    failCycles: number
    restoreCycles: number
    restoreGap: string
  }
  upstreams: DnsUpstream[]
}

export interface DnsUpstreamsRes {
  capable: boolean
  // форк rev14+: можно править политику автопроверки (health) из панели;
  // отсутствует/ложь - редактор политики скрыт (старый форк)
  healthCapable?: boolean
  state?: DnsUpstreamsState
}

// Политика каскада DNS (46-док §3, 66-док): уровни L1-L5 mawg компонирует
// в порядок апстримов MT (один PUT в форк); гистерезис переключений -
// в форке (канарейка K/M/restoreGap).
export interface DnsCascadePolicy {
  enabled: boolean
  primary?: string[] // L1: основные (host[:port]); с tunnel - L2 через пул
  tunnel?: string // пул для пресета dns-tunnel; пусто - основные напрямую
  fallback?: string[] // L3: запасные напрямую
  yandex?: boolean // L4: 77.88.8.8
  provider?: boolean // L5: DNS провайдера (resolv.conf.auto, срез при применении)
}

export interface DnsPolicyRes {
  capable: boolean
  dohCapable?: boolean // MT rev11+: уровни DoT (tls://) и DoH (https://)
  policy: DnsCascadePolicy
  composed?: string[] // порядок из сохранённой политики
  active?: string[] // живой порядок апстримов MT
  tunnelGroup?: { id?: string; interface?: string }
}

// Зонный DNS (80-док, форк rev13): доменная зона -> свои упорядоченные
// DNS-серверы, путь до них - через пул (группа dnszone-*, заводится
// применением). FallbackOpen=false = fail-closed: все серверы зоны
// недоступны - зона молчит (SERVFAIL), имена не утекают в общий DNS.
export interface DnsZone {
  id: string
  zone: string
  upstreams: string[]
  pool?: string
  fallbackOpen?: boolean
  enable: boolean
}

export interface DnsZonesRes {
  capable: boolean
  zones: DnsZone[]
  live?: {
    available: boolean
    zones: { zone: string; fallback: boolean; upstreams: DnsUpstream[] }[]
  }
  warnings?: string[]
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
  // итог пробы ПУЛА (managed-строки): те же данные, что в карточке пула
  probeOk?: boolean | null
  probeMs?: number
  probeAt?: number
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
  // источник установленного ядра sing-box (lx|extended|upstream) - диалог
  // установки предвыбирает его
  source?: string
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
  api?: boolean
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
  // ядро sing-box: источник установки - диалог показывает «источник: ...»
  source?: string
  sourceTitle?: string
}

// ядро VPN для карточки «Ядра VPN» (GET /cores): lifecycle-менеджмент
export interface CoreInfo {
  id: string
  title: string
  installed: boolean
  version?: string
  lx?: boolean
  extended?: boolean
  source?: string // источник сборки sing-box: lx | extended | upstream
  mode?: string // own | shared
  running: boolean
  autostart: boolean
  autostartNote?: string
  managedByMawg: boolean
  disabled: boolean
  note?: string
  caps: { start: boolean; stop: boolean; enable: boolean; disable: boolean; uninstall: boolean; install?: boolean }
}

export interface SystemCheckRes {
  platform: string
  items: SystemItem[]
}

// GET /cores/singbox/sources: источники установки ядра sing-box
export interface SingboxSourceInfo {
  id: string
  title: string
  note: string
  latest?: string
  upx: boolean
  available: boolean
  error?: string
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
  // узлы, идущие в движок sing-box (легаси renderSourcePlan)
  engine?: { tag: string; type: string }[]
  // выданный конфиг Amnezia (обмен ключа / Free)
  amnezia?: {
    protocol?: string
    serverCountry?: string
    serverCountryName?: string
    maxDevices?: number
    activeDevices?: number
    issuedConfigs?: number
    availableCountries?: { code: string; name?: string }[]
  }
}

// --- наборы интерфейсов (ротация) ---

export interface BundleView {
  name: string
  members: string[]
  groups: string[]
  currentMember?: string
  memberDevices: Record<string, string>
}

// --- пулы: слоты/разбор ссылок/состав (Ф3.5б) ---

// GET /slots (platform.SlotInfo)
export interface SlotInfo {
  id: string
  device: string
  description: string
  linkUp: boolean
  connected: boolean
  managed: boolean
  address?: string
}

// POST /links/inspect (web/linksapi.go)
export interface LinkInspect {
  error?: string
  native: number
  engine: number
  types?: string[]
  tags?: string[]
  userinfo?: string
  intervalHours?: number
  warnings?: string[]
  amnezia?: {
    serviceType: string
    serviceProtocol: string
  }
}

// GET /pools/{name}/members (web/members.go)
export interface PoolMember {
  name: string
  proto: string
  detail?: string
  endpoint?: string
  active?: boolean
  enabled: boolean
}

// GET /pools/{name}/engine: узлы движкового пула из clash_api движка
// (60-док). mode: "urltest" (авто-лучший пинг) | "selector" (ручной
// выбор); delay - последний успешный замер, мс (0 = не измерен/мёртв).
// type/transport - протокол узла и транспорт (VLESS/grpc; слово
// владельца 2026-10-17: у подключений разнобой - показывать тип).
export interface EngineNode {
  tag: string
  delay: number
  type?: string
  transport?: string
  off?: boolean // исключён из пула (в конфиг движка не попадает)
}

export interface PoolEngine {
  mode: string
  groupMode?: string // режим по настройкам mawg: urltest | selector | rotate
  now: string
  nodes: EngineNode[]
}
