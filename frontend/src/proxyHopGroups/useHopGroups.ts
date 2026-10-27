// 链式代理（fork）：独立首跳组 UI 文案与选项，避免扩散上游页面逻辑。
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { hopGroupsAPI, type HopGroup } from './api'

const en = {
  title: 'First-hop groups',
  create: 'New group',
  edit: 'Edit group',
  name: 'Name',
  strategy: 'Strategy',
  fallback: 'Fallback · first healthy node',
  urlTest: 'URL test · lowest latency',
  loadBalance: 'Load balance · round-robin per connection',
  loadBalanceHint:
    'Each new connection goes to the next healthy node; unhealthy nodes are skipped. HTTP keep-alive and HTTP/2 reuse one connection, so requests on it stay on one node. If the landing proxy allows only certain source IPs, allow every first-hop node. Hashing and sticky modes are not offered: the first hop always targets the same landing proxy, so hashing would send all traffic to one node.',
  balancedSelection: 'Round-robin across healthy nodes',
  activeConnections: 'Active connections',
  testURL: 'Health-check URL',
  interval: 'Health-check interval (seconds)',
  tolerance: 'Latency tolerance (ms)',
  updateInterval: 'Subscription refresh (seconds)',
  subscriptions: 'Subscription URLs · one per line',
  subscriptionHint:
    'URLs contain credentials and are never returned by the server.',
  replaceSubscriptions: 'Replace saved subscription URLs',
  keepSubscriptions:
    'Leave replacement disabled to keep the saved URLs. An empty replacement clears them.',
  manual: 'Manual members · direct proxies only',
  noManual: 'No active direct proxies available.',
  active: 'Active',
  inactive: 'Inactive',
  status: 'Status',
  save: 'Save',
  cancel: 'Cancel',
  remove: 'Delete',
  confirmDelete:
    'Delete this first-hop group? Referenced groups cannot be deleted.',
  refresh: 'Refresh status',
  selected: 'Selected node',
  noneSelected: 'No healthy node selected',
  nodes: 'Nodes',
  alive: 'Alive',
  down: 'Unavailable',
  delay: 'Delay',
  providers: 'Subscriptions',
  updated: 'Last successful update',
  count: 'Nodes',
  never: 'Not updated',
  empty:
    'No first-hop groups yet. Add a subscription or manual members to get started.',
  choose: 'Select first-hop group',
  noDefault: 'No default first-hop group',
  groupDefault: 'Default first-hop group',
  failClosed:
    'When every node is unavailable, traffic fails closed. There is no direct fallback.',
  loadFailed: 'Could not load first-hop groups.',
  saveFailed: 'Could not save first-hop group.',
  deleteFailed: 'Could not delete first-hop group.',
  saved: 'First-hop group saved.',
  deleted: 'First-hop group deleted.',
  selectRequired: 'Select a first-hop group.',
  loading: 'Loading…',
  close: 'Close',
  subscriptionSaved: 'Saved subscriptions',
  groupMode: 'First-hop group',
  defaultHint: 'A group and a single default proxy are mutually exclusive.',
  currentDefault: 'Default first-hop group',
  memberOrder:
    'Fallback prefers nodes in member order, followed by subscription nodes. Health checks run in the background.',
  unavailableMember: 'Unavailable member'
}
type Copy = typeof en
const zh: Copy = {
  title: '首跳组',
  create: '新建首跳组',
  edit: '编辑首跳组',
  name: '名称',
  strategy: '切换策略',
  fallback: 'Fallback · 优先首个健康节点',
  urlTest: 'URL test · 优先低延迟节点',
  loadBalance: 'Load balance · 按连接轮询',
  loadBalanceHint:
    '每个新连接依次分给下一个健康节点，不健康的节点会被跳过。HTTP keep-alive 和 HTTP/2 会复用同一条连接，复用期间的请求留在同一节点。落地代理若有来源 IP 白名单，需要放行所有首跳节点。不提供哈希和粘性模式：首跳的目标恒为同一个落地代理，哈希会把流量全部压到一个节点。',
  balancedSelection: '在健康节点间轮询',
  activeConnections: '活动连接',
  testURL: '健康检查地址',
  interval: '健康检查间隔（秒）',
  tolerance: '延迟容差（毫秒）',
  updateInterval: '订阅更新间隔（秒）',
  subscriptions: '订阅 URL · 每行一个',
  subscriptionHint: '订阅地址含凭据，服务器不会返回原文。',
  replaceSubscriptions: '替换已保存的订阅地址',
  keepSubscriptions: '不勾选则保留已有地址；勾选后留空则清空订阅。',
  manual: '手动成员 · 仅直连代理',
  noManual: '暂无可用的直连代理。',
  active: '启用',
  inactive: '停用',
  status: '状态',
  save: '保存',
  cancel: '取消',
  remove: '删除',
  confirmDelete: '确认删除此首跳组？仍被引用的首跳组不能删除。',
  refresh: '刷新状态',
  selected: '当前选中节点',
  noneSelected: '暂无健康节点',
  nodes: '节点',
  alive: '存活',
  down: '不可用',
  delay: '延迟',
  providers: '订阅',
  updated: '最近成功更新时间',
  count: '节点数',
  never: '尚未更新',
  empty: '暂无首跳组。可添加机场订阅或选择手动节点。',
  choose: '选择首跳组',
  noDefault: '不使用默认首跳组',
  groupDefault: '默认首跳组',
  failClosed: '所有节点不可用时拨号失败，绝不回退直连。',
  loadFailed: '加载首跳组失败。',
  saveFailed: '保存首跳组失败。',
  deleteFailed: '删除首跳组失败。',
  saved: '首跳组已保存。',
  deleted: '首跳组已删除。',
  selectRequired: '请选择首跳组。',
  loading: '加载中…',
  close: '关闭',
  subscriptionSaved: '已保存订阅数',
  groupMode: '首跳组',
  defaultHint: '默认首跳组与单个默认前置代理二选一。',
  currentDefault: '当前默认首跳组',
  memberOrder:
    'Fallback 优先按手动成员顺序选择，再选择订阅节点；健康检查在后台持续执行。',
  unavailableMember: '不可用成员'
}
export function useHopGroupText() {
  const { locale } = useI18n()
  return computed(() => (locale.value.startsWith('zh') ? zh : en))
}
export function useHopGroups() {
  const groups = ref<HopGroup[]>([])
  const text = useHopGroupText()
  const error = ref('')
  async function loadHopGroups() {
    try {
      groups.value = await hopGroupsAPI.list()
      error.value = ''
    } catch {
      error.value = text.value.loadFailed
    }
  }
  onMounted(loadHopGroups)
  const groupOptions = computed(() =>
    groups.value
      .filter((group) => group.status === 'active')
      .map((group) => ({ label: group.name, value: group.id }))
  )
  return { groups, groupOptions, loadHopGroups, error, text }
}
