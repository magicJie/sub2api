<template>
  <!-- 加密协议参数（ss/vmess/vless/trojan），按协议/传输/TLS 模式条件展示 -->
  <fieldset
    v-if="isEncryptedProtocol(protocol)"
    class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600"
  >
    <legend class="px-1 text-sm font-medium text-gray-700 dark:text-gray-300">
      {{ t('admin.proxies.encryptedOptions') }}
    </legend>

    <!-- ss -->
    <div v-if="protocol === 'ss'">
      <label :for="fid('method')" class="input-label">{{ t('admin.proxies.opt.method') }}</label>
      <Select
        :id="fid('method')"
        :aria-label="t('admin.proxies.opt.method')"
        :model-value="effective.method"
        :options="toOptions(SS_METHODS)"
        :placeholder="t('admin.proxies.opt.methodPlaceholder')"
        @update:model-value="(v) => setField('method', v)"
      />
    </div>

    <template v-if="hasTransport(protocol)">
      <!-- vmess -->
      <div v-if="protocol === 'vmess'">
        <label :for="fid('security')" class="input-label">{{ t('admin.proxies.opt.security') }}</label>
        <Select
          :id="fid('security')"
          :aria-label="t('admin.proxies.opt.security')"
          :model-value="effective.security"
          :options="toOptions(VMESS_SECURITIES)"
          @update:model-value="(v) => setField('security', v)"
        />
      </div>

      <!-- vless -->
      <div v-if="protocol === 'vless'" class="grid grid-cols-2 gap-4">
        <div>
          <label :for="fid('flow')" class="input-label">{{ t('admin.proxies.opt.flow') }}</label>
          <Select
            :id="fid('flow')"
            :aria-label="t('admin.proxies.opt.flow')"
            :model-value="effective.flow"
            :options="flowOptions"
            @update:model-value="(v) => setField('flow', v)"
          />
        </div>
        <div>
          <label :for="fid('encryption')" class="input-label">{{ t('admin.proxies.opt.encryption') }}</label>
          <input
            :id="fid('encryption')"
            :value="effective.encryption"
            type="text"
            class="input"
            placeholder="none"
            @input="onInput('encryption', $event)"
          />
        </div>
      </div>

      <!-- 传输层 -->
      <div>
        <label :for="fid('network')" class="input-label">{{ t('admin.proxies.opt.network') }}</label>
        <Select
          :id="fid('network')"
          :aria-label="t('admin.proxies.opt.network')"
          :model-value="effective.network"
          :options="toOptions(NETWORKS)"
          @update:model-value="(v) => setField('network', v)"
        />
      </div>
      <div v-if="'path' in effective" class="grid grid-cols-2 gap-4">
        <div>
          <label :for="fid('path')" class="input-label">{{ t('admin.proxies.opt.path') }}</label>
          <input
            :id="fid('path')"
            :value="effective.path"
            type="text"
            class="input"
            placeholder="/"
            @input="onInput('path', $event)"
          />
        </div>
        <div>
          <label :for="fid('host')" class="input-label">{{ t('admin.proxies.opt.host') }}</label>
          <input
            :id="fid('host')"
            :value="effective.host"
            type="text"
            class="input"
            :placeholder="t('admin.proxies.opt.optional')"
            @input="onInput('host', $event)"
          />
        </div>
      </div>
      <div v-if="'service_name' in effective">
        <label :for="fid('service_name')" class="input-label">{{ t('admin.proxies.opt.serviceName') }}</label>
        <input
          :id="fid('service_name')"
          :value="effective.service_name"
          type="text"
          class="input"
          @input="onInput('service_name', $event)"
        />
      </div>
      <div v-if="'mode' in effective">
        <label :for="fid('mode')" class="input-label">{{ t('admin.proxies.opt.mode') }}</label>
        <Select
          :id="fid('mode')"
          :aria-label="t('admin.proxies.opt.mode')"
          :model-value="effective.mode"
          :options="toOptions(XHTTP_MODES)"
          @update:model-value="(v) => setField('mode', v)"
        />
      </div>

      <!-- 安全层 -->
      <div>
        <label :for="fid('tls')" class="input-label">{{ t('admin.proxies.opt.tls') }}</label>
        <Select
          :id="fid('tls')"
          :aria-label="t('admin.proxies.opt.tls')"
          :model-value="effective.tls"
          :options="tlsOptions"
          @update:model-value="(v) => setField('tls', v)"
        />
      </div>
      <template v-if="effective.tls !== 'none'">
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label :for="fid('sni')" class="input-label">{{ t('admin.proxies.opt.sni') }}</label>
            <input
              :id="fid('sni')"
              :value="effective.sni"
              type="text"
              class="input"
              :placeholder="t('admin.proxies.opt.optional')"
              @input="onInput('sni', $event)"
            />
          </div>
          <div>
            <label :for="fid('alpn')" class="input-label">{{ t('admin.proxies.opt.alpn') }}</label>
            <input
              :id="fid('alpn')"
              :value="effective.alpn"
              type="text"
              class="input"
              placeholder="h2,http/1.1"
              @input="onInput('alpn', $event)"
            />
          </div>
        </div>
        <div>
          <label :for="fid('fingerprint')" class="input-label">{{ t('admin.proxies.opt.fingerprint') }}</label>
          <Select
            :id="fid('fingerprint')"
            :aria-label="t('admin.proxies.opt.fingerprint')"
            :model-value="effective.fingerprint"
            :options="fingerprintOptions"
            @update:model-value="(v) => setField('fingerprint', v)"
          />
        </div>
      </template>
      <template v-if="effective.tls === 'reality'">
        <div>
          <label :for="fid('public_key')" class="input-label">{{ t('admin.proxies.opt.publicKey') }}</label>
          <input
            :id="fid('public_key')"
            :value="effective.public_key"
            type="text"
            required
            class="input font-mono"
            @input="onInput('public_key', $event)"
          />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label :for="fid('short_id')" class="input-label">{{ t('admin.proxies.opt.shortId') }}</label>
            <input
              :id="fid('short_id')"
              :value="effective.short_id"
              type="text"
              class="input font-mono"
              :placeholder="t('admin.proxies.opt.optional')"
              @input="onInput('short_id', $event)"
            />
          </div>
          <div>
            <label :for="fid('spider_x')" class="input-label">{{ t('admin.proxies.opt.spiderX') }}</label>
            <input
              :id="fid('spider_x')"
              :value="effective.spider_x"
              type="text"
              class="input"
              :placeholder="t('admin.proxies.opt.optional')"
              @input="onInput('spider_x', $event)"
            />
          </div>
        </div>
      </template>
    </template>
  </fieldset>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import {
  FINGERPRINTS,
  NETWORKS,
  SS_METHODS,
  TLS_MODES,
  VMESS_SECURITIES,
  XHTTP_MODES,
  hasTransport,
  isEncryptedProtocol,
  resolveProxyOptions,
  type ProxyOptions
} from '@/utils/proxyProtocol'

const props = defineProps<{
  protocol: string
  modelValue: ProxyOptions
  /** 表单内 id 前缀，保证 create/edit 两个表单的 label for 不冲突 */
  idPrefix: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: ProxyOptions): void
}>()

const { t } = useI18n()

const fid = (key: string) => `${props.idPrefix}-opt-${key}`

// 合并协议默认值后的展示值；只有用户改动过的键才写回 modelValue
const effective = computed(() => resolveProxyOptions(props.protocol, props.modelValue))

const toOptions = (values: string[]) => values.map((value) => ({ value, label: value }))

const flowOptions = computed(() => [
  { value: '', label: t('admin.proxies.opt.none') },
  { value: 'xtls-rprx-vision', label: 'xtls-rprx-vision' }
])

const tlsOptions = computed(() =>
  TLS_MODES.map((value) => ({
    value,
    label: value === 'none' ? t('admin.proxies.opt.none') : value.toUpperCase()
  }))
)

const fingerprintOptions = computed(() => {
  const opts = toOptions(FINGERPRINTS)
  // reality 必须指定 fingerprint；普通 TLS 允许留空走默认
  if (effective.value.tls === 'reality') return opts
  return [{ value: '', label: t('admin.proxies.opt.default') }, ...opts]
})

const setField = (key: string, value: unknown) => {
  emit('update:modelValue', { ...props.modelValue, [key]: value == null ? '' : String(value) })
}

const onInput = (key: string, event: Event) => {
  setField(key, (event.target as HTMLInputElement).value)
}
</script>
