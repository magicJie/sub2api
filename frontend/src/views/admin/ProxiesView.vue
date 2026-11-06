<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <!-- Left: Search + Filters -->
          <div class="relative w-full sm:w-64">
            <Icon
              name="search"
              size="md"
              class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
            />
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('admin.proxies.searchProxies')"
              class="input pl-10"
              @input="handleSearch"
            />
          </div>

          <div class="w-full sm:w-40">
            <Select
              v-model="filters.protocol"
              :options="protocolOptions"
              :placeholder="t('admin.proxies.allProtocols')"
              @change="handleFilterChange"
            />
          </div>
          <div class="w-full sm:w-36">
            <Select
              v-model="filters.status"
              :options="statusOptions"
              :placeholder="t('admin.proxies.allStatus')"
              @change="handleFilterChange"
            />
          </div>

          <!-- Right: All action buttons -->
          <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
            <button
              @click="loadProxies"
              :disabled="loading"
              class="btn btn-secondary"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button
              @click="handleBatchTest"
              :disabled="batchTesting || loading"
              class="btn btn-secondary"
              :title="t('admin.proxies.testConnection')"
            >
              <Icon name="play" size="md" class="mr-2" />
              {{ t('admin.proxies.testConnection') }}
            </button>
            <button
              @click="handleBatchQualityCheck"
              :disabled="batchQualityChecking || loading"
              class="btn btn-secondary"
              :title="t('admin.proxies.batchQualityCheck')"
            >
              <Icon name="shield" size="md" class="mr-2" :class="batchQualityChecking ? 'animate-pulse' : ''" />
              {{ t('admin.proxies.batchQualityCheck') }}
            </button>
            <button
              @click="openBatchDelete"
              :disabled="selectedCount === 0"
              class="btn btn-danger"
              :title="t('admin.proxies.batchDeleteAction')"
            >
              <Icon name="trash" size="md" class="mr-2" />
              {{ t('admin.proxies.batchDeleteAction') }}
            </button>
            <button @click="showImportData = true" class="btn btn-secondary">
              {{ t('admin.proxies.dataImport') }}
            </button>
            <button @click="showExportDataDialog = true" class="btn btn-secondary">
              {{ selectedCount > 0 ? t('admin.proxies.dataExportSelected') : t('admin.proxies.dataExport') }}
            </button>
            <button @click="showProxyPools = true" class="btn btn-secondary">
              <Icon name="grid" size="md" class="mr-2" />
              {{ t('admin.proxyPools.manage') }}
            </button>
            <UpstreamManager @changed="loadProxies" /> <!-- 前置组（fork） -->
            <button @click="openCreateModal" class="btn btn-primary">
              <Icon name="plus" size="md" class="mr-2" />
              {{ t('admin.proxies.createProxy') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <div ref="proxyTableRef" class="flex min-h-0 flex-1 flex-col overflow-hidden">
        <DataTable
          :columns="columns"
          :data="proxies"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="id"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #header-select>
            <input
              type="checkbox"
              class="h-4 w-4 cursor-pointer rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="allVisibleSelected"
              @click.stop
              @change="toggleSelectAllVisible($event)"
            />
          </template>

          <template #cell-select="{ row }">
            <input
              type="checkbox"
              class="h-4 w-4 cursor-pointer rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="selectedProxyIds.has(row.id)"
              @click.stop
              @change="toggleSelectRow(row.id, $event)"
            />
          </template>

          <template #cell-name="{ value }">
            <div class="flex items-center gap-1.5">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
            </div>
          </template>

          <template #cell-protocol="{ value }">
            <span
              v-if="value"
              :class="['badge', protocolBadgeClass(value)]"
            >
              {{ value.toUpperCase() }}
            </span>
            <span v-else class="text-sm text-gray-400">-</span>
          </template>

          <template #cell-address="{ row }">
            <div class="flex items-center gap-1.5">
              <code class="code text-xs">{{ row.host }}:{{ row.port }}</code>
              <div class="relative">
                <button
                  type="button"
                  class="rounded p-0.5 text-gray-400 hover:text-primary-600 dark:hover:text-primary-400"
                  :title="t('admin.proxies.copyProxyUrl')"
                  @click.stop="copyProxyUrl(row)"
                  @contextmenu.prevent="toggleCopyMenu(row.id)"
                >
                  <Icon name="copy" size="sm" />
                </button>
                <!-- 右键展开格式选择菜单 -->
                <div
                  v-if="copyMenuProxyId === row.id"
                  class="absolute left-0 top-full z-50 mt-1 w-auto min-w-[180px] rounded-lg border border-gray-200 bg-white py-1 shadow-lg dark:border-dark-500 dark:bg-dark-700"
                >
                  <button
                    v-for="fmt in getCopyFormats(row)"
                    :key="fmt.label"
                    class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-gray-100 dark:hover:bg-dark-600"
                    @click.stop="copyFormat(fmt.value)"
                  >
                    <span class="truncate font-mono text-gray-600 dark:text-gray-300">{{ fmt.label }}</span>
                  </button>
                </div>
              </div>
            </div>
            <div
              v-if="row.chain_error"
              class="mt-0.5 max-w-[240px] truncate text-xs text-red-600 dark:text-red-400"
              :title="row.chain_error"
            >
              {{ t('admin.proxies.chainErrorInline', { error: row.chain_error }) }}
            </div>
            <div
              v-else-if="row.chain && row.chain.length > 0"
              class="mt-0.5 max-w-[240px] truncate text-xs text-gray-500 dark:text-gray-400"
              :title="formatChain(row)"
            >
              {{ formatChain(row) }}
            </div>
          </template>

          <template #cell-auth="{ row }">
            <div v-if="row.username || row.password" class="flex items-center gap-1.5">
              <div class="flex flex-col text-xs">
                <span v-if="row.username" class="text-gray-700 dark:text-gray-200">{{ row.username }}</span>
                <span v-if="row.password" class="font-mono text-gray-500 dark:text-gray-400">
                  {{ visiblePasswordIds.has(row.id) ? row.password : '••••••' }}
                </span>
              </div>
              <button
                v-if="row.password"
                type="button"
                class="ml-1 rounded p-0.5 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                @click.stop="visiblePasswordIds.has(row.id) ? visiblePasswordIds.delete(row.id) : visiblePasswordIds.add(row.id)"
              >
                <Icon :name="visiblePasswordIds.has(row.id) ? 'eyeOff' : 'eye'" size="sm" />
              </button>
            </div>
            <span v-else class="text-sm text-gray-400">-</span>
          </template>

          <template #cell-location="{ row }">
            <div class="flex items-center gap-2">
              <img
                v-if="row.country_code"
                :src="flagUrl(row.country_code)"
                :alt="row.country || row.country_code"
                class="h-4 w-6 rounded-sm"
              />
              <span v-if="formatLocation(row)" class="text-sm text-gray-700 dark:text-gray-200">
                {{ formatLocation(row) }}
              </span>
              <span v-else class="text-sm text-gray-400">-</span>
            </div>
          </template>

          <template #cell-account_count="{ row, value }">
            <button
              v-if="(value || 0) > 0"
              type="button"
              class="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-primary-700 hover:bg-gray-200 dark:bg-dark-600 dark:text-primary-300 dark:hover:bg-dark-500"
              @click="openAccountsModal(row)"
            >
              {{ t('admin.groups.accountsCount', { count: value || 0 }) }}
            </button>
            <span
              v-else
              class="inline-flex items-center rounded bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
            >
              {{ t('admin.groups.accountsCount', { count: 0 }) }}
            </span>
          </template>

          <template #cell-latency="{ row }">
            <div class="flex flex-col gap-1">
              <span
                v-if="row.latency_status === 'failed'"
                class="badge badge-danger"
                :title="row.latency_message || undefined"
              >
                {{ t('admin.proxies.latencyFailed') }}
              </span>
              <span
                v-else-if="typeof row.latency_ms === 'number'"
                :class="['badge', row.latency_ms < 200 ? 'badge-success' : 'badge-warning']"
              >
                {{ row.latency_ms }}ms
              </span>
              <span v-else class="text-sm text-gray-400">-</span>
              <div
                v-if="typeof row.quality_checked === 'number'"
                class="flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400"
                :title="row.quality_summary || undefined"
              >
                <span>{{ qualityInlineText(row) }}</span>
                <span class="badge" :class="qualityOverallClass(row.quality_status, row)">
                  {{ qualityOverallLabel(row.quality_status, row) }}
                </span>
              </div>
            </div>
          </template>

          <template #cell-expiry="{ row }">
            <span v-if="!row.expires_at" class="text-sm text-gray-400">{{ t('admin.proxies.neverExpires') }}</span>
            <div v-else class="flex flex-col text-xs">
              <span class="text-gray-700 dark:text-gray-200">{{ formatDateTime(row.expires_at) }}</span>
              <span :class="expiryBadgeClass(row)">{{ expiryLabel(row) }}</span>
            </div>
          </template>

          <template #cell-created_at="{ row }">
            <span class="text-xs text-gray-600 dark:text-gray-300">{{ formatDateTime(row.created_at) }}</span>
          </template>

          <template #cell-status="{ value }">
            <span
              :class="[
                'badge',
                value === 'active' ? 'badge-success' : value === 'expired' ? 'badge-danger' : 'badge-danger'
              ]"
            >
              {{ t('admin.accounts.status.' + value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                @click="handleTestConnection(row)"
                :disabled="testingProxyIds.has(row.id)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-emerald-50 hover:text-emerald-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-emerald-900/20 dark:hover:text-emerald-400"
              >
                <svg
                  v-if="testingProxyIds.has(row.id)"
                  class="h-4 w-4 animate-spin"
                  fill="none"
                  viewBox="0 0 24 24"
                >
                  <circle
                    class="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    stroke-width="4"
                  ></circle>
                  <path
                    class="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                  ></path>
                </svg>
                <Icon v-else name="checkCircle" size="sm" />
                <span class="text-xs">{{ t('admin.proxies.testConnection') }}</span>
              </button>
              <button
                @click="handleQualityCheck(row)"
                :disabled="qualityCheckingProxyIds.has(row.id)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-blue-900/20 dark:hover:text-blue-400"
              >
                <svg
                  v-if="qualityCheckingProxyIds.has(row.id)"
                  class="h-4 w-4 animate-spin"
                  fill="none"
                  viewBox="0 0 24 24"
                >
                  <circle
                    class="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    stroke-width="4"
                  ></circle>
                  <path
                    class="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                  ></path>
                </svg>
                <Icon v-else name="shield" size="sm" />
                <span class="text-xs">{{ t('admin.proxies.qualityCheck') }}</span>
              </button>
              <button
                @click="handleEdit(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                @click="handleDelete(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              >
                <Icon name="trash" size="sm" />
                <span class="text-xs">{{ t('common.delete') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.proxies.noProxiesYet')"
              :description="t('admin.proxies.createFirstProxy')"
              :action-text="t('admin.proxies.createProxy')"
              @action="openCreateModal"
            />
          </template>
        </DataTable>
        </div>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- Create Proxy Modal -->
    <BaseDialog
      :show="showCreateModal"
      :title="t('admin.proxies.createProxy')"
      width="normal"
      @close="closeCreateModal"
    >
      <!-- Tab Switch -->
      <div
        class="mb-6 flex items-center justify-between gap-3 border-b border-gray-200 dark:border-dark-600"
      >
        <div class="flex min-w-0 shrink-0">
          <button
            type="button"
            @click="createMode = 'standard'"
            :class="[
              '-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors',
              createMode === 'standard'
                ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
            ]"
          >
            <Icon name="plus" size="sm" class="mr-1.5 inline" />
            {{ t('admin.proxies.standardAdd') }}
          </button>
          <button
            type="button"
            @click="createMode = 'batch'"
            :class="[
              '-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors',
              createMode === 'batch'
                ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
            ]"
          >
            <svg
              class="mr-1.5 inline h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.5"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M3.75 12h16.5m-16.5 3.75h16.5M3.75 19.5h16.5M5.625 4.5h12.75a1.875 1.875 0 010 3.75H5.625a1.875 1.875 0 010-3.75z"
              />
            </svg>
            {{ t('admin.proxies.batchAdd') }}
          </button>
        </div>
        <ProxyAdBanner />
      </div>

      <!-- Standard Add Form -->
      <form
        v-if="createMode === 'standard'"
        id="create-proxy-form"
        @submit.prevent="handleCreateProxy"
        class="space-y-5"
      >
        <div>
          <label for="create-share-link" class="input-label">{{ t('admin.proxies.shareLink') }}</label>
          <div class="flex gap-2">
            <input
              id="create-share-link"
              v-model="createShareLink"
              type="text"
              class="input flex-1 font-mono text-sm"
              :placeholder="t('admin.proxies.shareLinkPlaceholder')"
              @keydown.enter.prevent="handleParseLink('create')"
            />
            <button
              type="button"
              class="btn btn-secondary shrink-0"
              :disabled="parsingLink || !createShareLink.trim()"
              @click="handleParseLink('create')"
            >
              {{ parsingLink ? t('admin.proxies.parsingLink') : t('admin.proxies.parseLink') }}
            </button>
          </div>
          <p class="input-hint mt-1">{{ t('admin.proxies.shareLinkHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.name') }}</label>
          <input
            v-model="createForm.name"
            type="text"
            required
            class="input"
            :placeholder="t('admin.proxies.enterProxyName')"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.protocol') }}</label>
          <Select
            v-model="createForm.protocol"
            :aria-label="t('admin.proxies.protocol')"
            :options="protocolSelectOptions"
            @change="createForm.options = {}"
          />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.proxies.host') }}</label>
            <input
              v-model="createForm.host"
              type="text"
              required
              :placeholder="t('admin.proxies.form.hostPlaceholder')"
              class="input"
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.proxies.port') }}</label>
            <input
              v-model.number="createForm.port"
              type="number"
              required
              min="1"
              max="65535"
              :placeholder="t('admin.proxies.form.portPlaceholder')"
              class="input"
            />
          </div>
        </div>
        <div v-if="!isEncryptedProtocol(createForm.protocol)">
          <label class="input-label">{{ t('admin.proxies.username') }}</label>
          <input
            v-model="createForm.username"
            type="text"
            class="input"
            :placeholder="t('admin.proxies.optionalAuth')"
          />
        </div>
        <div>
          <label for="create-proxy-password" class="input-label">{{ secretLabel(createForm.protocol) }}</label>
          <div class="relative">
            <input
              id="create-proxy-password"
              v-model="createForm.password"
              :type="createPasswordVisible ? 'text' : 'password'"
              class="input pr-10"
              :required="isEncryptedProtocol(createForm.protocol)"
              :placeholder="secretPlaceholder(createForm.protocol, t('admin.proxies.optionalAuth'))"
            />
            <button
              type="button"
              class="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
              :aria-label="t('admin.proxies.toggleSecretVisibility')"
              @click="createPasswordVisible = !createPasswordVisible"
            >
              <Icon :name="createPasswordVisible ? 'eyeOff' : 'eye'" size="md" />
            </button>
          </div>
        </div>
        <ProxyProtocolOptionsFields
          v-model="createForm.options"
          :protocol="createForm.protocol"
          id-prefix="create"
        />
        <fieldset class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <legend class="px-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.proxies.upstreamSection') }}
          </legend>
          <div>
            <label for="create-upstream-mode" class="input-label">{{ t('admin.proxies.upstreamMode') }}</label>
            <Select
              id="create-upstream-mode"
              v-model="createForm.upstream_mode"
              :aria-label="t('admin.proxies.upstreamMode')"
              :options="upstreamModeOptions"
            />
          </div>
          <UpstreamGroupSelect v-if="createForm.upstream_mode === 'group'" id="create-upstream-group" v-model="createForm.upstream_group_id" /> <!-- 前置组（fork） -->
        </fieldset>
        <div>
          <label class="input-label">{{ t('admin.proxies.expiresAt') }}</label>
          <div class="mb-2 flex flex-wrap gap-2">
            <button
              v-for="d in EXPIRY_PRESETS"
              :key="d"
              type="button"
              class="btn btn-sm"
              :class="createForm.expires_at === addDaysToBase('', d) ? 'btn-primary' : 'btn-secondary'"
              @click="createExpiresDays = d"
            >
              {{ t('admin.proxies.nDays', { days: d }) }}
            </button>
          </div>
          <input
            v-model.number="createExpiresDays"
            type="number"
            min="0"
            class="input mb-2"
            :placeholder="t('admin.proxies.expiryDaysPlaceholder')"
          />
          <input v-model="createForm.expires_at" type="date" max="9999-12-31" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.fallbackMode') }}</label>
          <Select v-model="createForm.fallback_mode" :options="[
            { label: t('admin.proxies.fallbackNone'), value: 'none' },
            { label: t('admin.proxies.fallbackProxy'), value: 'proxy' },
            { label: t('admin.proxies.fallbackDirect'), value: 'direct' },
          ]" />
        </div>
        <div v-if="createForm.fallback_mode === 'proxy'">
          <label class="input-label">{{ t('admin.proxies.backupProxy') }}</label>
          <Select v-model="createForm.backup_proxy_id" :options="backupProxyOptions()" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxyPools.pool') }}</label>
          <Select v-model="createForm.pool_id" :options="proxyPoolOptions" />
        </div>

      </form>

      <!-- Batch Add Form -->
      <div v-else class="space-y-5">
        <div>
          <label class="input-label">{{ t('admin.proxies.batchInput') }}</label>
          <textarea
            v-model="batchInput"
            rows="10"
            class="input font-mono text-sm"
            :placeholder="t('admin.proxies.batchInputPlaceholder')"
            @input="parseBatchInput"
          ></textarea>
          <p class="input-hint mt-2">
            {{ t('admin.proxies.batchInputHint') }}
          </p>
        </div>

        <!-- Parse Result -->
        <div v-if="batchParseResult.total > 0" class="rounded-lg bg-gray-50 p-4 dark:bg-dark-700">
            <div class="flex items-center gap-4 text-sm">
              <div class="flex items-center gap-1.5">
              <Icon name="checkCircle" size="sm" :stroke-width="2" class="text-primary-500" />
              <span class="text-gray-700 dark:text-gray-300">
                {{ t('admin.proxies.parsedCount', { count: batchParseResult.valid }) }}
              </span>
            </div>
            <div v-if="batchParseResult.invalid > 0" class="flex items-center gap-1.5">
              <Icon
                name="exclamationCircle"
                size="sm"
                :stroke-width="2"
                class="text-amber-500"
              />
              <span class="text-amber-600 dark:text-amber-400">
                {{ t('admin.proxies.invalidCount', { count: batchParseResult.invalid }) }}
              </span>
            </div>
            <div v-if="batchParseResult.duplicate > 0" class="flex items-center gap-1.5">
              <svg
                class="h-4 w-4 text-gray-400"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="2"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75"
                />
              </svg>
              <span class="text-gray-500 dark:text-gray-400">
                {{ t('admin.proxies.duplicateCount', { count: batchParseResult.duplicate }) }}
              </span>
            </div>
          </div>
        </div>

      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeCreateModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            v-if="createMode === 'standard'"
            type="submit"
            form="create-proxy-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            <svg
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            {{ submitting ? t('admin.proxies.creating') : t('common.create') }}
          </button>
          <button
            v-else
            @click="handleBatchCreate"
            type="button"
            :disabled="submitting || batchParseResult.valid === 0"
            class="btn btn-primary"
          >
            <svg
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            {{
              submitting
                ? t('admin.proxies.importing')
                : t('admin.proxies.importProxies', { count: batchParseResult.valid })
            }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Proxy Modal -->
    <BaseDialog
      :show="showEditModal"
      :title="t('admin.proxies.editProxy')"
      width="normal"
      @close="closeEditModal"
    >
      <form
        v-if="editingProxy"
        id="edit-proxy-form"
        @submit.prevent="handleUpdateProxy"
        class="space-y-5"
      >
        <div>
          <label for="edit-share-link" class="input-label">{{ t('admin.proxies.shareLink') }}</label>
          <div class="flex gap-2">
            <input
              id="edit-share-link"
              v-model="editShareLink"
              type="text"
              class="input flex-1 font-mono text-sm"
              :placeholder="t('admin.proxies.shareLinkPlaceholder')"
              @keydown.enter.prevent="handleParseLink('edit')"
            />
            <button
              type="button"
              class="btn btn-secondary shrink-0"
              :disabled="parsingLink || !editShareLink.trim()"
              @click="handleParseLink('edit')"
            >
              {{ parsingLink ? t('admin.proxies.parsingLink') : t('admin.proxies.parseLink') }}
            </button>
          </div>
          <p class="input-hint mt-1">{{ t('admin.proxies.shareLinkHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.name') }}</label>
          <input v-model="editForm.name" type="text" required class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.protocol') }}</label>
          <Select
            v-model="editForm.protocol"
            :aria-label="t('admin.proxies.protocol')"
            :options="protocolSelectOptions"
            @change="editForm.options = {}"
          />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.proxies.host') }}</label>
            <input v-model="editForm.host" type="text" required class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.proxies.port') }}</label>
            <input
              v-model.number="editForm.port"
              type="number"
              required
              min="1"
              max="65535"
              class="input"
            />
          </div>
        </div>
        <div v-if="!isEncryptedProtocol(editForm.protocol)">
          <label class="input-label">{{ t('admin.proxies.username') }}</label>
          <input v-model="editForm.username" type="text" class="input" />
        </div>
        <div>
          <label for="edit-proxy-password" class="input-label">{{ secretLabel(editForm.protocol) }}</label>
          <div class="relative">
            <input
              id="edit-proxy-password"
              v-model="editForm.password"
              :type="editPasswordVisible ? 'text' : 'password'"
              :placeholder="t('admin.proxies.leaveEmptyToKeep')"
              class="input pr-10"
              @input="editPasswordDirty = true"
            />
            <button
              type="button"
              class="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
              :aria-label="t('admin.proxies.toggleSecretVisibility')"
              @click="editPasswordVisible = !editPasswordVisible"
            >
              <Icon :name="editPasswordVisible ? 'eyeOff' : 'eye'" size="md" />
            </button>
          </div>
        </div>
        <ProxyProtocolOptionsFields
          v-model="editForm.options"
          :protocol="editForm.protocol"
          id-prefix="edit"
        />
        <fieldset class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <legend class="px-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.proxies.upstreamSection') }}
          </legend>
          <div>
            <label for="edit-upstream-mode" class="input-label">{{ t('admin.proxies.upstreamMode') }}</label>
            <Select
              id="edit-upstream-mode"
              v-model="editForm.upstream_mode"
              :aria-label="t('admin.proxies.upstreamMode')"
              :options="upstreamModeOptions"
            />
          </div>
          <UpstreamGroupSelect v-if="editForm.upstream_mode === 'group'" id="edit-upstream-group" v-model="editForm.upstream_group_id" /> <!-- 前置组（fork） -->
        </fieldset>
        <div>
          <label class="input-label">{{ t('admin.proxies.status') }}</label>
          <Select v-model="editForm.status" :options="editStatusOptions" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.expiresAt') }}</label>
          <div class="mb-2 flex flex-wrap gap-2">
            <button
              v-for="d in EXPIRY_PRESETS"
              :key="d"
              type="button"
              class="btn btn-sm"
              :class="editForm.expires_at === addDaysToBase(editBaseDate, d) ? 'btn-primary' : 'btn-secondary'"
              @click="editExpiresDays = d"
            >
              {{ t('admin.proxies.nDays', { days: d }) }}
            </button>
          </div>
          <input
            v-model.number="editExpiresDays"
            type="number"
            min="0"
            class="input mb-2"
            :placeholder="t('admin.proxies.expiryDaysPlaceholder')"
          />
          <input v-model="editForm.expires_at" type="date" max="9999-12-31" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.fallbackMode') }}</label>
          <Select v-model="editForm.fallback_mode" :options="[
            { label: t('admin.proxies.fallbackNone'), value: 'none' },
            { label: t('admin.proxies.fallbackProxy'), value: 'proxy' },
            { label: t('admin.proxies.fallbackDirect'), value: 'direct' },
          ]" />
        </div>
        <div v-if="editForm.fallback_mode === 'proxy'">
          <label class="input-label">{{ t('admin.proxies.backupProxy') }}</label>
          <Select v-model="editForm.backup_proxy_id" :options="backupProxyOptions(editingProxy?.id)" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxyPools.pool') }}</label>
          <Select v-model="editForm.pool_id" :options="proxyPoolOptions" />
        </div>

      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeEditModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            v-if="editingProxy"
            type="submit"
            form="edit-proxy-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            <svg
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            {{ submitting ? t('admin.proxies.updating') : t('common.update') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.proxies.deleteProxy')"
      :message="t('admin.proxies.deleteConfirm', { name: deletingProxy?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Batch Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showBatchDeleteDialog"
      :title="t('admin.proxies.batchDelete')"
      :message="t('admin.proxies.batchDeleteConfirm', { count: selectedCount })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmBatchDelete"
      @cancel="showBatchDeleteDialog = false"
    />
    <ConfirmDialog
      :show="showExportDataDialog"
      :title="t('admin.proxies.dataExport')"
      :message="t('admin.proxies.dataExportConfirmMessage')"
      :confirm-text="t('admin.proxies.dataExportConfirm')"
      :cancel-text="t('common.cancel')"
      @confirm="handleExportData"
      @cancel="showExportDataDialog = false"
    />

    <ImportDataModal
      :show="showImportData"
      @close="showImportData = false"
      @imported="handleDataImported"
    />

    <!-- 代理池（fork） -->
    <ProxyPoolManagerModal :show="showProxyPools" @close="showProxyPools = false" @changed="handleProxyPoolsChanged" />

    <BaseDialog
      :show="showQualityReportDialog"
      :title="t('admin.proxies.qualityReportTitle')"
      width="wide"
      @close="closeQualityReportDialog"
    >
      <div v-if="qualityReport" class="space-y-4">
        <!-- Header Summary & Advisory Recommendation Status -->
        <div class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-700">
          <div class="flex flex-col gap-3">
            <div class="min-w-0 flex-1">
              <div class="text-sm font-medium text-gray-900 dark:text-white">
                {{ qualityReportProxy?.name || '-' }}
              </div>
              <div class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                {{ formatQualityReportSummary(qualityReport) }}
              </div>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <span class="badge max-w-full whitespace-normal" :class="qualityReportBaseClass(qualityReport)">
                {{ t('admin.proxies.qualityBaseQualificationLabel') }}: {{ qualityReportBaseLabel(qualityReport) }}
              </span>
              <span class="badge max-w-full whitespace-normal" :class="qualityReportEligibleClass(qualityReport)">
                {{ qualityReportEligibleLabel(qualityReport) }}
              </span>
            </div>
          </div>

          <div class="mt-2.5 rounded border border-gray-200 bg-white px-3 py-2 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200">
            <div>
              <span class="font-medium">{{ t('admin.proxies.qualityAdmissionLabel') }}:</span>
              <span class="ml-1">{{ qualityReportAdmissionReason(qualityReport) }}</span>
            </div>
            <div class="mt-1 text-gray-500 dark:text-gray-400">
              {{ t('admin.proxies.qualityAdvisoryNotice') }}
            </div>
          </div>

          <!-- Explicit Unauthenticated Boundary vs Real Login/Inference Notice -->
          <div class="mt-2.5 rounded border border-blue-200 bg-blue-50/70 px-3 py-2 text-xs text-blue-900 dark:border-blue-900/50 dark:bg-blue-950/30 dark:text-blue-200">
            <div class="font-semibold">{{ t('admin.proxies.qualityBoundaryNoticeTitle') }}</div>
            <div class="mt-0.5 leading-relaxed text-blue-800 dark:text-blue-300">
              {{ t('admin.proxies.qualityBoundaryNoticeBody') }}
            </div>
          </div>

          <div class="mt-3 grid grid-cols-2 gap-2 text-xs text-gray-600 dark:text-gray-300 sm:grid-cols-4">
            <div>
              <span class="text-gray-500 dark:text-gray-400">{{ t('admin.proxies.qualityExitIP') }}:</span>
              <span class="ml-1 font-mono font-medium text-gray-800 dark:text-gray-100">{{ qualityReport.exit_ip || '-' }}</span>
            </div>
            <div>
              <span class="text-gray-500 dark:text-gray-400">{{ t('admin.proxies.qualityCountry') }}:</span>
              <span class="ml-1 font-medium text-gray-800 dark:text-gray-100">{{ qualityReport.country || '-' }}</span>
            </div>
            <div>
              <span class="text-gray-500 dark:text-gray-400">{{ t('admin.proxies.qualityBaseLatency') }}:</span>
              <span class="ml-1 font-medium text-gray-800 dark:text-gray-100">
                {{ typeof qualityReport.base_latency_ms === 'number' ? `${qualityReport.base_latency_ms}ms` : '-' }}
              </span>
            </div>
            <div>
              <span class="text-gray-500 dark:text-gray-400">{{ t('admin.proxies.qualityCheckedAt') }}:</span>
              <span class="ml-1">{{ formatUnixTimestamp(qualityReport.checked_at) }}</span>
            </div>
            <div
              v-if="qualityReport.reputation?.valid_until && qualityReport.reputation.valid_until > 0"
              class="col-span-2 sm:col-span-4"
            >
              <span class="text-gray-500 dark:text-gray-400">{{ t('admin.proxies.qualityValidUntil') }}:</span>
              <span class="ml-1">{{ formatUnixTimestamp(qualityReport.reputation.valid_until) }}</span>
            </div>
          </div>
        </div>

        <!-- Prominent AI Provider & Surface Comparison (Claude / ChatGPT / Gemini / Grok) -->
        <div class="space-y-3 rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.proxies.qualityAIComparisonTitle') }}
              </h4>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.proxies.qualityAIComparisonSubtitle') }}
              </p>
            </div>
          </div>

          <div
            v-if="!hasAnyAIAssessments(qualityReport)"
            class="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          >
            {{ t('admin.proxies.qualityAIMissingBanner') }}
          </div>

          <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <div
              v-for="group in buildQualityAIProviderGroups(qualityReport)"
              :key="group.platform"
              class="flex flex-col rounded-lg border border-gray-200 bg-gray-50/70 p-3 dark:border-dark-600 dark:bg-dark-700/50"
            >
              <div class="flex items-center justify-between gap-2 border-b border-gray-200 pb-2 dark:border-dark-600">
                <div class="text-sm font-semibold text-gray-900 dark:text-white">
                  {{ group.label }}
                </div>
                <span class="badge" :class="group.badgeClass">
                  {{ group.summaryText }}
                </span>
              </div>

              <div class="mt-2.5 divide-y divide-gray-200 dark:divide-dark-600">
                <div
                  v-for="row in group.rows"
                  :key="row.target"
                  class="space-y-1.5 py-2.5 first:pt-0 last:pb-0"
                >
                  <div class="flex flex-wrap items-center justify-between gap-2">
                    <div class="flex flex-wrap items-center gap-1.5">
                      <span class="badge" :class="qualitySurfaceBadgeClass(row.surface)">
                        {{ qualitySurfaceLabel(row.surface) }}
                      </span>
                      <span class="text-xs font-medium text-gray-900 dark:text-white">
                        {{ qualityTargetLabel(row.target) }}
                      </span>
                    </div>
                    <div class="flex flex-wrap items-center gap-1.5">
                      <span class="badge" :class="qualityAIStatusBadgeClass(row.status, row.eligible)">
                        {{ qualityAIStatusLabel(row.status) }}
                      </span>
                      <span class="badge" :class="row.candidateBadgeClass">
                        {{ row.candidateLabel }}
                      </span>
                    </div>
                  </div>

                  <div class="text-[11px] text-gray-500 dark:text-gray-400">
                    {{ row.scopeHint }}
                  </div>

                  <!-- Evidence, Error Code, HTTP Status, Latency, Retry-After, Observed At -->
                  <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                    <div class="flex items-center gap-1">
                      <span class="badge" :class="qualityAIEvidenceBadgeClass(row.evidence, row.eligible)">
                        {{ qualityAIEvidenceLabel(row.evidence) }}
                      </span>
                      <span v-if="row.evidence && row.evidence !== 'missing'" class="font-mono text-[11px] text-gray-400 dark:text-gray-500">
                        [{{ row.evidence }}]
                      </span>
                    </div>
                    <span class="text-gray-600 dark:text-gray-300">
                      HTTP: <strong class="font-mono">{{ row.httpStatus ?? '-' }}</strong>
                    </span>
                    <span class="text-gray-600 dark:text-gray-300">
                      {{ t('admin.proxies.qualityTableLatency') }}:
                      <strong class="font-mono">{{ typeof row.latencyMs === 'number' ? `${row.latencyMs}ms` : '-' }}</strong>
                    </span>
                    <span
                      v-if="row.errorCode"
                      class="rounded bg-gray-200/80 px-1.5 py-0.5 font-mono text-[11px] text-gray-800 dark:bg-dark-600 dark:text-gray-200"
                    >
                      {{ t('admin.proxies.qualityFieldErrorCode') }}: {{ row.errorCode }}
                    </span>
                    <span
                      v-if="row.retryAfter"
                      class="rounded bg-amber-100 px-1.5 py-0.5 font-mono text-[11px] text-amber-800 dark:bg-amber-950/60 dark:text-amber-300"
                    >
                      {{ t('admin.proxies.qualityFieldRetryAfter') }}: {{ row.retryAfter }}
                    </span>
                    <span
                      v-if="row.cfRay"
                      class="font-mono text-[11px] text-gray-400 dark:text-gray-500"
                    >
                      cf-ray: {{ row.cfRay }}
                    </span>
                    <span
                      v-if="row.observedAt"
                      class="text-[11px] text-gray-500 dark:text-gray-400"
                    >
                      {{ t('admin.proxies.qualityFieldObservedAt') }}: {{ formatUnixTimestamp(row.observedAt) }}
                    </span>
                  </div>

                  <!-- Localized Reason -->
                  <div class="text-xs text-gray-700 dark:text-gray-200">
                    {{ row.reason }}
                  </div>

                  <!-- Scope / Non-fraud / Boundary Callout -->
                  <div
                    v-if="row.scopeCallout"
                    class="rounded px-2 py-1 text-[11px]"
                    :class="row.scopeCalloutClass"
                  >
                    {{ row.scopeCallout }}
                  </div>

                  <!-- Endpoint & Final URL / Redirect -->
                  <div
                    v-if="row.url || row.finalUrl"
                    class="flex flex-wrap items-center gap-x-3 gap-y-0.5 break-all font-mono text-[11px] text-gray-500 dark:text-gray-400"
                  >
                    <span v-if="row.url">
                      {{ t('admin.proxies.qualityFieldEndpoint') }}: {{ row.url }}
                    </span>
                    <span
                      v-if="row.isRedirected"
                      class="font-semibold text-amber-700 dark:text-amber-400"
                    >
                      {{ t('admin.proxies.qualityFieldRedirectedTo') }}: {{ row.finalUrl }}
                    </span>
                    <span v-else-if="row.finalUrl && row.finalUrl !== row.url">
                      {{ t('admin.proxies.qualityFieldFinalUrl') }}: {{ row.finalUrl }}
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Other Non-probed Providers Base Qualification Note -->
          <div class="flex flex-wrap items-center justify-between gap-2 rounded border border-gray-200 bg-gray-50 px-3 py-2 text-xs dark:border-dark-600 dark:bg-dark-700/40">
            <div>
              <span class="font-medium text-gray-800 dark:text-gray-200">{{ t('admin.proxies.qualityPlatformOther') }}:</span>
              <span class="ml-1 text-gray-600 dark:text-gray-300">{{ t('admin.proxies.qualityPlatformOtherHint') }}</span>
            </div>
            <span class="badge shrink-0" :class="qualityReportBaseClass(qualityReport)">
              {{ qualityReportBaseLabel(qualityReport) }}
            </span>
          </div>
        </div>

        <!-- Supporting Context: Generic Connectivity & IP Reputation -->
        <div class="space-y-3">
          <div class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('admin.proxies.qualitySupportingSectionTitle') }}
          </div>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <!-- Connectivity & Comprehensive Quality -->
            <div class="rounded-lg border border-gray-200 bg-white p-3.5 dark:border-dark-600 dark:bg-dark-800">
              <div class="flex items-start justify-between gap-2">
                <div>
                  <div class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                    {{ t('admin.proxies.qualityConnectivitySectionTitle') }}
                  </div>
                  <div class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                    {{ t('admin.proxies.qualityScaleHint') }}
                  </div>
                </div>
                <div v-if="isReputationUnknown(qualityReport)" class="text-right">
                  <span class="badge badge-warning">
                    {{ t('admin.proxies.qualityReputationInsufficient') }}
                  </span>
                </div>
                <div v-else class="text-right">
                  <div class="text-xl font-semibold text-gray-900 dark:text-white">
                    {{ qualityReport.score }}<span class="text-xs font-normal text-gray-400">/100</span>
                  </div>
                  <div class="text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.proxies.qualityGrade', { grade: qualityReport.grade }) }}
                  </div>
                </div>
              </div>
              <div class="mt-2 text-xs text-gray-600 dark:text-gray-300">
                <div v-if="isReputationUnknown(qualityReport)" class="text-amber-600 dark:text-amber-400">
                  {{ t('admin.proxies.qualityUnknownNoScoreHint') }}
                </div>
                <div class="mt-1">
                  {{
                    t('admin.proxies.qualityCountsSummary', {
                      passed: qualityReport.passed_count,
                      warn: qualityReport.warn_count,
                      challenge: qualityReport.challenge_count,
                      failed: qualityReport.failed_count
                    })
                  }}
                </div>
              </div>
            </div>

            <!-- IP Risk & Reputation -->
            <div class="rounded-lg border border-gray-200 bg-white p-3.5 dark:border-dark-600 dark:bg-dark-800">
              <div class="flex items-start justify-between gap-2">
                <div>
                  <div class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                    {{ t('admin.proxies.qualityRiskSectionTitle') }}
                  </div>
                  <div class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                    {{ t('admin.proxies.qualityRiskScaleHint') }}
                  </div>
                </div>
                <div v-if="isReputationUnknown(qualityReport)" class="text-right">
                  <span class="badge badge-warning">
                    {{ t('admin.proxies.qualityReputationInsufficient') }}
                  </span>
                </div>
                <div v-else-if="typeof qualityReport.reputation?.risk_score === 'number'" class="text-right">
                  <div class="text-xl font-semibold" :class="qualityReport.reputation.risk_score >= 50 ? 'text-amber-600 dark:text-amber-400' : 'text-gray-900 dark:text-white'">
                    {{ t('admin.proxies.qualityRiskScoreDisplay', { score: qualityReport.reputation.risk_score }) }}
                  </div>
                  <span
                    class="badge mt-0.5"
                    :class="qualityReport.reputation.risk_score >= 50 ? 'badge-warning' : 'badge-success'"
                  >
                    {{ qualityReport.reputation.risk_score >= 50 ? t('admin.proxies.qualityRiskLevelHigh') : t('admin.proxies.qualityRiskLevelLow') }}
                  </span>
                </div>
              </div>
              <div class="mt-2 text-xs" :class="isReputationUnknown(qualityReport) ? 'text-amber-600 dark:text-amber-400' : 'text-gray-600 dark:text-gray-300'">
                {{ translateQualityReason(qualityReport.reputation?.reason || qualityReport.admission_reason) || t('admin.proxies.qualityReputationMissingReason') }}
              </div>
            </div>
          </div>

          <!-- Reputation Signals, Multi-source Observations & IPOK Attribution -->
          <div class="space-y-3 rounded-lg border border-gray-200 bg-gray-50 p-3.5 text-xs dark:border-dark-600 dark:bg-dark-700/60">
            <div>
              <div class="font-medium text-gray-700 dark:text-gray-200">
                {{ t('admin.proxies.qualityRiskSignals') }}
              </div>
              <div v-if="qualityReport.reputation?.signals?.length" class="mt-1.5 flex flex-wrap gap-1.5">
                <span
                  v-for="signal in qualityReport.reputation.signals"
                  :key="signal"
                  class="badge"
                  :class="reputationSignalBadgeClass(signal)"
                >
                  {{ signal }}
                </span>
              </div>
              <div v-else class="mt-1 text-gray-500 dark:text-gray-400">
                {{ isReputationUnknown(qualityReport) ? t('admin.proxies.qualitySignalsUnavailable') : t('admin.proxies.qualitySignalsNone') }}
              </div>
            </div>

            <div>
              <div class="font-medium text-gray-700 dark:text-gray-200">
                {{ t('admin.proxies.qualityRiskSources') }}
              </div>
              <div v-if="qualityReport.reputation?.sources?.length" class="mt-1.5 divide-y divide-gray-200 rounded border border-gray-200 bg-white dark:divide-dark-600 dark:border-dark-600 dark:bg-dark-800">
                <div
                  v-for="src in qualityReport.reputation.sources"
                  :key="src.source"
                  class="flex flex-wrap items-center justify-between gap-2 px-3 py-2"
                >
                  <div class="flex items-center gap-2">
                    <span class="font-medium text-gray-900 dark:text-white">{{ src.source }}</span>
                    <span
                      v-if="typeof src.risk_score === 'number'"
                      class="badge"
                      :class="sourceRiskBadgeClass(src, qualityReport.checked_at)"
                    >
                      {{ t('admin.proxies.qualitySourceRiskScore', { score: src.risk_score }) }}
                    </span>
                    <span v-else class="text-gray-400 dark:text-gray-500">
                      {{ t('admin.proxies.qualitySourceNoScore') }}
                    </span>
                  </div>
                  <div
                    :class="sourceObservationClass(src, qualityReport.checked_at)"
                    :title="src.observed_at || undefined"
                  >
                    {{ formatSourceObservation(src, qualityReport.checked_at) }}
                  </div>
                </div>
              </div>
              <div v-else class="mt-1 text-amber-600 dark:text-amber-400">
                {{ t('admin.proxies.qualitySourcesMissing') }}
              </div>
            </div>

            <div class="flex flex-wrap items-center justify-between gap-2 border-t border-gray-200 pt-2 text-gray-500 dark:border-dark-600 dark:text-gray-400">
              <span>
                {{ t('admin.proxies.qualityAttributionPrefix') }}
                <a
                  href="https://ipok.io"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="font-medium text-primary-600 underline hover:text-primary-700 dark:text-primary-400"
                >IPOK (https://ipok.io)</a>
              </span>
              <a
                href="https://ipok.io/methodology"
                target="_blank"
                rel="noopener noreferrer"
                class="text-primary-600 underline hover:text-primary-700 dark:text-primary-400"
              >
                {{ t('admin.proxies.qualityMethodologyLink') }} (https://ipok.io/methodology)
              </a>
            </div>
          </div>
        </div>

        <!-- Raw Probe Items Diagnostic Table -->
        <div class="space-y-1.5">
          <div class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
            {{ t('admin.proxies.qualityProbeTableTitle') }}
          </div>
          <div class="max-h-80 overflow-auto rounded-lg border border-gray-200 dark:border-dark-600">
            <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
              <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400">
                <tr>
                  <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableTarget') }}</th>
                  <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableSurface') }}</th>
                  <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableStatus') }}</th>
                  <th class="whitespace-nowrap px-3 py-2 text-left">HTTP</th>
                  <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableEvidence') }}</th>
                  <th class="whitespace-nowrap px-3 py-2 text-left">{{ t('admin.proxies.qualityTableLatency') }}</th>
                  <th class="px-3 py-2 text-left">{{ t('admin.proxies.qualityTableMessage') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
                <tr v-for="item in qualityReport.items" :key="item.target">
                  <td class="whitespace-nowrap px-3 py-2 text-gray-900 dark:text-white">
                    <div>{{ qualityTargetLabel(item.target) }}</div>
                  </td>
                  <td class="whitespace-nowrap px-3 py-2">
                    <span class="badge whitespace-nowrap" :class="qualitySurfaceBadgeClass(item.surface)">
                      {{ qualitySurfaceLabel(item.surface) }}
                    </span>
                  </td>
                  <td class="whitespace-nowrap px-3 py-2">
                    <span class="badge whitespace-nowrap" :class="qualityStatusClass(item.status)">
                      {{ qualityStatusLabel(item.status) }}
                    </span>
                  </td>
                  <td class="whitespace-nowrap px-3 py-2 font-mono text-xs text-gray-600 dark:text-gray-300">
                    {{ item.http_status ?? '-' }}
                  </td>
                  <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                    <div v-if="item.evidence" class="flex flex-col gap-0.5">
                      <span>{{ qualityAIEvidenceLabel(item.evidence) }}</span>
                      <span class="font-mono text-[11px] text-gray-400">{{ item.evidence }}</span>
                    </div>
                    <span v-else class="text-gray-400">-</span>
                    <div v-if="item.error_code" class="mt-0.5 font-mono text-[11px] text-amber-700 dark:text-amber-400">
                      {{ t('admin.proxies.qualityFieldErrorCode') }}: {{ item.error_code }}
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                    <div>{{ typeof item.latency_ms === 'number' ? `${item.latency_ms}ms` : '-' }}</div>
                    <div v-if="item.observed_at" class="text-[11px] text-gray-400">
                      {{ formatUnixTimestamp(item.observed_at) }}
                    </div>
                  </td>
                  <td class="px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                    <div>{{ translateQualityReason(item.message, item.evidence) || '-' }}</div>
                    <div v-if="item.retry_after" class="mt-0.5 font-mono text-[11px] text-amber-700 dark:text-amber-400">
                      {{ t('admin.proxies.qualityFieldRetryAfter') }}: {{ item.retry_after }}
                    </div>
                    <div v-if="item.cf_ray" class="mt-0.5 font-mono text-[11px] text-gray-400">
                      cf-ray: {{ item.cf_ray }}
                    </div>
                    <div v-if="item.url || item.final_url" class="mt-0.5 break-all font-mono text-[11px] text-gray-400">
                      <span v-if="item.url">{{ item.url }}</span>
                      <span v-if="item.final_url && item.final_url !== item.url" class="ml-1 text-amber-700 dark:text-amber-400">
                        → {{ item.final_url }}
                      </span>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button @click="closeQualityReportDialog" class="btn btn-secondary">
            {{ t('common.close') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Proxy Accounts Dialog -->
    <BaseDialog
      :show="showAccountsModal"
      :title="t('admin.proxies.accountsTitle', { name: accountsProxy?.name || '' })"
      width="normal"
      @close="closeAccountsModal"
    >
      <div v-if="accountsLoading" class="flex items-center justify-center py-8 text-sm text-gray-500">
        <Icon name="refresh" size="md" class="mr-2 animate-spin" />
        {{ t('common.loading') }}
      </div>
      <div v-else-if="proxyAccounts.length === 0" class="py-6 text-center text-sm text-gray-500">
        {{ t('admin.proxies.accountsEmpty') }}
      </div>
      <div v-else class="max-h-80 overflow-auto">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="px-4 py-2 text-left">{{ t('admin.proxies.accountName') }}</th>
              <th class="px-4 py-2 text-left">{{ t('admin.accounts.columns.platformType') }}</th>
              <th class="px-4 py-2 text-left">{{ t('admin.proxies.accountNotes') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-for="account in proxyAccounts" :key="account.id">
              <td class="px-4 py-2 font-medium text-gray-900 dark:text-white">{{ account.name }}</td>
              <td class="px-4 py-2">
                <PlatformTypeBadge :platform="account.platform" :type="account.type" />
              </td>
              <td class="px-4 py-2 text-gray-600 dark:text-gray-300">
                {{ account.notes || '-' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button @click="closeAccountsModal" class="btn btn-secondary">
            {{ t('common.close') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  Proxy,
  ProxyAccountSummary,
  ProxyAIPlatformAssessment,
  ProxyIPReputationSource,
  ProxyProtocol,
  ProxyQualityCheckItem,
  ProxyQualityCheckResult,
  ProxyUpstreamMode
} from '@/types'
import type { BatchCreateProxyItem } from '@/api/admin/proxies'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ImportDataModal from '@/components/admin/proxy/ImportDataModal.vue'
import ProxyProtocolOptionsFields from '@/components/admin/proxy/ProxyProtocolOptionsFields.vue'
import ProxyPoolManagerModal from '@/proxyPool/components/ProxyPoolManagerModal.vue' // 代理池（fork）
import { useProxyPoolOptions } from '@/proxyPool/useProxyPools' // 代理池（fork）
import UpstreamManager from '@/proxyUpstreamGroups/UpstreamManager.vue' // 前置组（fork）
import UpstreamGroupSelect from '@/proxyUpstreamGroups/UpstreamGroupSelect.vue' // 前置组（fork）
import Select from '@/components/common/Select.vue'
import ProxyAdBanner from '@/components/common/ProxyAdBanner.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useSwipeSelect } from '@/composables/useSwipeSelect'
import { useTableSelection } from '@/composables/useTableSelection'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatDateTime } from '@/utils/format'
import { proxyExpiryBadgeClass, proxyExpiryLabelKey } from '@/utils/proxyExpiry'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  buildProxyOptions,
  isEncryptedProtocol,
  isShareLink,
  usesUuidSecret,
  validateProxySecret,
  type ProxyOptions
} from '@/utils/proxyProtocol'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const columns = computed<Column[]>(() => [
  { key: 'select', label: '', sortable: false },
  { key: 'name', label: t('admin.proxies.columns.name'), sortable: true },
  { key: 'protocol', label: t('admin.proxies.columns.protocol'), sortable: true },
  { key: 'address', label: t('admin.proxies.columns.address'), sortable: false },
  { key: 'auth', label: t('admin.proxies.columns.auth'), sortable: false },
  { key: 'location', label: t('admin.proxies.columns.location'), sortable: false },
  { key: 'account_count', label: t('admin.proxies.columns.accounts'), sortable: true },
  { key: 'latency', label: t('admin.proxies.columns.latency'), sortable: false },
  { key: 'expiry', label: t('admin.proxies.columns.expiry'), sortable: true },
  { key: 'created_at', label: t('admin.proxies.columns.createdAt'), sortable: true },
  { key: 'status', label: t('admin.proxies.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.proxies.columns.actions'), sortable: false }
])

// Filter options
const protocolOptions = computed(() => [
  { value: '', label: t('admin.proxies.allProtocols') },
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'socks5h', label: 'SOCKS5H' },
  { value: 'ss', label: 'Shadowsocks' },
  { value: 'vmess', label: 'VMess' },
  { value: 'vless', label: 'VLESS' },
  { value: 'trojan', label: 'Trojan' }
])

const statusOptions = computed(() => [
  { value: '', label: t('admin.proxies.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'expired', label: t('admin.proxies.expired') }
])

// Form options
const protocolSelectOptions = computed(() => [
  { value: 'http', label: t('admin.proxies.protocols.http') },
  { value: 'https', label: t('admin.proxies.protocols.https') },
  { value: 'socks5', label: t('admin.proxies.protocols.socks5') },
  { value: 'socks5h', label: t('admin.proxies.protocols.socks5h') },
  { value: 'ss', label: t('admin.proxies.protocols.ss') },
  { value: 'vmess', label: t('admin.proxies.protocols.vmess') },
  { value: 'vless', label: t('admin.proxies.protocols.vless') },
  { value: 'trojan', label: t('admin.proxies.protocols.trojan') }
])

// 前置组（fork）：前置只有直连或前置组
const upstreamModeOptions = computed(() => [
  { value: 'direct', label: t('admin.proxies.upstreamDirect') },
  { value: 'group', label: t('admin.proxies.upstreamGroup') }
])

const protocolBadgeClass = (protocol: string) => {
  if (isEncryptedProtocol(protocol)) return 'badge-purple'
  if (protocol.startsWith('socks5')) return 'badge-primary'
  return 'badge-gray'
}

// vmess/vless 的密钥字段是 UUID；ss/trojan 为密码(必填)
const secretLabel = (protocol: string) => {
  if (usesUuidSecret(protocol)) return 'UUID'
  if (isEncryptedProtocol(protocol)) return t('admin.proxies.secretPassword')
  return t('admin.proxies.password')
}
const secretPlaceholder = (protocol: string, plainPlaceholder: string) =>
  isEncryptedProtocol(protocol) ? '' : plainPlaceholder

// 链路展示:经 A → B → 本代理
const formatChain = (row: Proxy) =>
  t('admin.proxies.chainVia', { hops: (row.chain || []).map((hop) => hop.name).join(' → ') })

const editStatusOptions = computed(() => [
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') }
])

const proxies = ref<Proxy[]>([])
const visiblePasswordIds = reactive(new Set<number>())
const copyMenuProxyId = ref<number | null>(null)
const loading = ref(false)
const searchQuery = ref('')
const filters = reactive({
  protocol: '',
  status: ''
})
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})
const sortState = reactive({
  sort_by: 'id',
  sort_order: 'desc' as 'asc' | 'desc'
})

const showCreateModal = ref(false)
const createPasswordVisible = ref(false)
const showEditModal = ref(false)
const editPasswordVisible = ref(false)
const editPasswordDirty = ref(false)
const showImportData = ref(false)
const showDeleteDialog = ref(false)
const showBatchDeleteDialog = ref(false)
const showExportDataDialog = ref(false)
const showAccountsModal = ref(false)
const submitting = ref(false)
const exportingData = ref(false)
const testingProxyIds = ref<Set<number>>(new Set())
const qualityCheckingProxyIds = ref<Set<number>>(new Set())
const batchTesting = ref(false)
const batchQualityChecking = ref(false)
const proxyTableRef = ref<HTMLElement | null>(null)
const {
  selectedSet: selectedProxyIds,
  selectedCount,
  allVisibleSelected,
  isSelected,
  select,
  deselect,
  clear: clearSelectedProxies,
  removeMany: removeSelectedProxies,
  toggleVisible,
  batchUpdate
} = useTableSelection<Proxy>({
  rows: proxies,
  getId: (proxy) => proxy.id
})
useSwipeSelect(proxyTableRef, {
  isSelected,
  select,
  deselect,
  batchUpdate
})
const accountsProxy = ref<Proxy | null>(null)
const proxyAccounts = ref<ProxyAccountSummary[]>([])
const accountsLoading = ref(false)
const editingProxy = ref<Proxy | null>(null)
const deletingProxy = ref<Proxy | null>(null)
const showQualityReportDialog = ref(false)
const qualityReportProxy = ref<Proxy | null>(null)
const qualityReport = ref<ProxyQualityCheckResult | null>(null)

// Batch import state
const createMode = ref<'standard' | 'batch'>('standard')
const batchInput = ref('')
const batchParseResult = reactive({
  total: 0,
  valid: 0,
  invalid: 0,
  duplicate: 0,
  proxies: [] as BatchCreateProxyItem[]
})

const createForm = reactive({
  name: '',
  protocol: 'http' as ProxyProtocol,
  host: '',
  port: 8080,
  username: '',
  password: '',
  expires_at: '' as string,
  fallback_mode: 'none' as 'none' | 'proxy' | 'direct',
  backup_proxy_id: null as number | null,
  expiry_warn_days: 7 as number,
  upstream_mode: 'direct' as ProxyUpstreamMode,
  upstream_group_id: null as number | null, // 前置组（fork）
  options: {} as ProxyOptions,
  pool_id: null as number | null, // 代理池（fork）：null 为默认池
})

const editForm = reactive({
  name: '',
  protocol: 'http' as ProxyProtocol,
  host: '',
  port: 8080,
  username: '',
  password: '',
  status: 'active' as 'active' | 'inactive' | 'expired',
  expires_at: '' as string,
  fallback_mode: 'none' as 'none' | 'proxy' | 'direct',
  backup_proxy_id: null as number | null,
  expiry_warn_days: 7 as number,
  upstream_mode: 'direct' as ProxyUpstreamMode,
  upstream_group_id: null as number | null, // 前置组（fork）
  options: {} as ProxyOptions,
  pool_id: null as number | null, // 代理池（fork）：null 为默认池
})

const allProxiesForBackup = ref<Proxy[]>([])
const loadBackupProxyOptions = async () => {
  allProxiesForBackup.value = await adminAPI.proxies.getAllWithCount()
}
const backupProxyOptions = (excludeId?: number) =>
  allProxiesForBackup.value
    .filter(p => p.id !== excludeId)
    .map(p => ({ label: `${p.name} (${p.host}:${p.port})`, value: p.id }))

// 代理池（fork）
const showProxyPools = ref(false)
const { poolOptions: proxyPoolOptions, loadPools: loadProxyPools } = useProxyPoolOptions()
const handleProxyPoolsChanged = () => {
  void loadProxyPools()
  loadProxies()
}
const openCreateModal = () => {
  void loadProxyPools() // 代理池（fork）
  showCreateModal.value = true
}

// ── 分享链接解析 ──
const createShareLink = ref('')
const editShareLink = ref('')
const parsingLink = ref(false)

const handleParseLink = async (target: 'create' | 'edit') => {
  const link = (target === 'create' ? createShareLink.value : editShareLink.value).trim()
  if (!link || parsingLink.value) return
  parsingLink.value = true
  try {
    const parsed = await adminAPI.proxies.parseLink(link)
    const form = target === 'create' ? createForm : editForm
    if (parsed.name && !form.name.trim()) form.name = parsed.name
    form.protocol = parsed.protocol
    form.host = parsed.host
    form.port = parsed.port
    form.username = parsed.username || ''
    form.password = parsed.password || ''
    form.options = { ...(parsed.options || {}) }
    if (target === 'edit') editPasswordDirty.value = true
    appStore.showSuccess(t('admin.proxies.linkParsed'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.linkParseFailed')))
    console.error('Error parsing proxy share link:', error)
  } finally {
    parsingLink.value = false
  }
}

// 前置组（fork）：直连时显式传 null 清除前置组
const upstreamPayload = (form: { upstream_mode: ProxyUpstreamMode; upstream_group_id: number | null }) => ({
  upstream_mode: form.upstream_mode,
  upstream_group_id: form.upstream_mode === 'group' ? form.upstream_group_id : null
})

let abortController: AbortController | null = null

const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const maybeError = error as { name?: string; code?: string }
  return maybeError.name === 'AbortError' || maybeError.code === 'ERR_CANCELED'
}

const toggleSelectRow = (id: number, event: Event) => {
  const target = event.target as HTMLInputElement
  if (target.checked) {
    select(id)
    return
  }
  deselect(id)
}

const toggleSelectAllVisible = (event: Event) => {
  const target = event.target as HTMLInputElement
  toggleVisible(target.checked)
}

const buildProxyQueryFilters = () => ({
  protocol: filters.protocol || undefined,
  status: (filters.status || undefined) as 'active' | 'inactive' | 'expired' | undefined,
  search: searchQuery.value || undefined,
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order
})

const loadProxies = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentAbortController = new AbortController()
  abortController = currentAbortController
  loading.value = true
  try {
    const response = await adminAPI.proxies.list(
      pagination.page,
      pagination.page_size,
      buildProxyQueryFilters(),
      { signal: currentAbortController.signal }
    )
    if (currentAbortController.signal.aborted || abortController !== currentAbortController) {
      return
    }
    proxies.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error) {
    if (isAbortError(error)) {
      return
    }
    appStore.showError(t('admin.proxies.failedToLoad'))
    console.error('Error loading proxies:', error)
  } finally {
    if (abortController === currentAbortController) {
      loading.value = false
      abortController = null
    }
  }
}

const handleFilterChange = () => {
  pagination.page = 1
  loadProxies()
}

let searchTimeout: ReturnType<typeof setTimeout>
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadProxies()
  }, 300)
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadProxies()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadProxies()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadProxies()
}

const closeCreateModal = () => {
  showCreateModal.value = false
  createMode.value = 'standard'
  createForm.name = ''
  createForm.protocol = 'http'
  createForm.host = ''
  createForm.port = 8080
  createForm.username = ''
  createForm.password = ''
  createForm.expires_at = ''
  createForm.fallback_mode = 'none'
  createForm.backup_proxy_id = null
  createForm.expiry_warn_days = 7
  createForm.upstream_mode = 'direct'
  createForm.upstream_group_id = null // 前置组（fork）
  createForm.options = {}
  createForm.pool_id = null // 代理池（fork）
  createShareLink.value = ''
  createPasswordVisible.value = false
  batchInput.value = ''
  batchParseResult.total = 0
  batchParseResult.valid = 0
  batchParseResult.invalid = 0
  batchParseResult.duplicate = 0
  batchParseResult.proxies = []
}

const handleDataImported = () => {
  showImportData.value = false
  loadProxies()
}

// Parse proxy URL: protocol://user:pass@host:port or protocol://host:port
// Host may be a domain, IPv4, or bracketed IPv6 ([2001:db8::1]).
const parseProxyUrl = (
  line: string
): {
  protocol: ProxyProtocol
  host: string
  port: number
  username: string
  password: string
} | null => {
  const trimmed = line.trim()
  if (!trimmed) return null

  // Regex to parse proxy URL (supports http, https, socks5, socks5h).
  // Host alternatives: [bracketed-IPv6] | hostname/IPv4 (colon-free, so the
  // match stops before the final :port).
  const regex =
    /^(https?|socks5h?):\/\/(?:([^:@\[\]]+):([^@\[\]]+)@)?(\[[0-9a-f:.]+\]|[^:\[\]]+):(\d+)$/i
  const match = trimmed.match(regex)

  if (!match) return null

  const [, protocol, username, password, rawHost, port] = match
  const portNum = parseInt(port, 10)

  if (portNum < 1 || portNum > 65535) return null

  // Strip brackets from IPv6 literals; the backend re-brackets via net.JoinHostPort.
  const host = rawHost.replace(/^\[|\]$/g, '').trim()

  return {
    protocol: protocol.toLowerCase() as ProxyProtocol,
    host,
    port: portNum,
    username: username?.trim() || '',
    password: password?.trim() || ''
  }
}

const parseBatchInput = () => {
  const lines = batchInput.value.split('\n').filter((l) => l.trim())
  const seen = new Set<string>()
  const proxies: typeof batchParseResult.proxies = []
  let invalid = 0
  let duplicate = 0

  for (const line of lines) {
    // 加密协议分享链接原样交给后端解析
    if (isShareLink(line)) {
      const link = line.trim()
      if (seen.has(link)) {
        duplicate++
        continue
      }
      seen.add(link)
      proxies.push({ link })
      continue
    }

    const parsed = parseProxyUrl(line)
    if (!parsed) {
      invalid++
      continue
    }

    // Check for duplicates (same host:port:username:password)
    const key = `${parsed.host}:${parsed.port}:${parsed.username}:${parsed.password}`
    if (seen.has(key)) {
      duplicate++
      continue
    }
    seen.add(key)
    proxies.push(parsed)
  }

  batchParseResult.total = lines.length
  batchParseResult.valid = proxies.length
  batchParseResult.invalid = invalid
  batchParseResult.duplicate = duplicate
  batchParseResult.proxies = proxies
}

const handleBatchCreate = async () => {
  if (batchParseResult.valid === 0) return

  submitting.value = true
  try {
    const result = await adminAPI.proxies.batchCreate(batchParseResult.proxies)
    const created = result.created || 0
    const skipped = result.skipped || 0

    if (created > 0) {
      appStore.showSuccess(t('admin.proxies.batchImportSuccess', { created, skipped }))
    } else {
      appStore.showInfo(t('admin.proxies.batchImportAllSkipped', { skipped }))
    }

    closeCreateModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.failedToImport')))
    console.error('Error batch creating proxies:', error)
  } finally {
    submitting.value = false
  }
}

const handleCreateProxy = async () => {
  if (!createForm.name.trim()) {
    appStore.showError(t('admin.proxies.nameRequired'))
    return
  }
  if (!createForm.host.trim()) {
    appStore.showError(t('admin.proxies.hostRequired'))
    return
  }
  if (createForm.port < 1 || createForm.port > 65535) {
    appStore.showError(t('admin.proxies.portInvalid'))
    return
  }
  const createOptions = buildProxyOptions(createForm.protocol, createForm.options)
  const createSecretError = validateProxySecret(createForm.protocol, createForm.password, createOptions)
  if (createSecretError) {
    appStore.showError(t(createSecretError))
    return
  }
  if (createForm.upstream_mode === 'group' && !createForm.upstream_group_id) {
    appStore.showError(t('admin.proxies.upstreamGroupRequired'))
    return
  }
  const encrypted = isEncryptedProtocol(createForm.protocol)
  submitting.value = true
  try {
    await adminAPI.proxies.create({
      name: createForm.name.trim(),
      protocol: createForm.protocol,
      host: createForm.host.trim(),
      port: createForm.port,
      username: encrypted ? null : createForm.username.trim() || null,
      password: createForm.password.trim() || null,
      ...upstreamPayload(createForm),
      options: createOptions,
      expires_at: createForm.expires_at ? Math.floor(new Date(createForm.expires_at).getTime() / 1000) : null,
      fallback_mode: createForm.fallback_mode,
      backup_proxy_id: createForm.fallback_mode === 'proxy' ? createForm.backup_proxy_id : null,
      expiry_warn_days: createForm.expiry_warn_days,
      pool_id: createForm.pool_id, // 代理池（fork）
    })
    appStore.showSuccess(t('admin.proxies.proxyCreated'))
    closeCreateModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.failedToCreate')))
    console.error('Error creating proxy:', error)
  } finally {
    submitting.value = false
  }
}

const handleEdit = (proxy: Proxy) => {
  editingProxy.value = proxy
  editForm.name = proxy.name
  editForm.protocol = proxy.protocol
  editForm.host = proxy.host
  editForm.port = proxy.port
  editForm.username = proxy.username || ''
  editForm.password = proxy.password || ''
  editForm.status = proxy.status === 'expired' ? 'inactive' : proxy.status
  editForm.expires_at = proxy.expires_at ? proxy.expires_at.slice(0, 10) : ''
  editForm.fallback_mode = proxy.fallback_mode || 'none'
  editForm.backup_proxy_id = proxy.backup_proxy_id ?? null
  editForm.expiry_warn_days = proxy.expiry_warn_days ?? 7
  editForm.upstream_mode = proxy.upstream_mode === 'group' ? 'group' : 'direct'
  editForm.upstream_group_id = proxy.upstream_group_id ?? null // 前置组（fork）
  editForm.options = { ...(proxy.options || {}) }
  editForm.pool_id = proxy.pool_id ?? null // 代理池（fork）
  void loadProxyPools()
  editShareLink.value = ''
  editPasswordVisible.value = false
  editPasswordDirty.value = false
  showEditModal.value = true
}

const closeEditModal = () => {
  showEditModal.value = false
  editingProxy.value = null
  editPasswordVisible.value = false
  editPasswordDirty.value = false
}

const handleUpdateProxy = async () => {
  if (!editingProxy.value) return
  if (!editForm.name.trim()) {
    appStore.showError(t('admin.proxies.nameRequired'))
    return
  }
  if (!editForm.host.trim()) {
    appStore.showError(t('admin.proxies.hostRequired'))
    return
  }
  if (editForm.port < 1 || editForm.port > 65535) {
    appStore.showError(t('admin.proxies.portInvalid'))
    return
  }
  const editOptions = buildProxyOptions(editForm.protocol, editForm.options)
  // 未修改密码时沿用已保存的密钥，交由后端校验
  const editSecretError = validateProxySecret(
    editForm.protocol,
    editForm.password,
    editOptions,
    !editPasswordDirty.value
  )
  if (editSecretError) {
    appStore.showError(t(editSecretError))
    return
  }
  if (editForm.upstream_mode === 'group' && !editForm.upstream_group_id) {
    appStore.showError(t('admin.proxies.upstreamGroupRequired'))
    return
  }

  submitting.value = true
  try {
    const updateData: any = {
      name: editForm.name.trim(),
      protocol: editForm.protocol,
      host: editForm.host.trim(),
      port: editForm.port,
      username: isEncryptedProtocol(editForm.protocol) ? '' : editForm.username.trim(),
      ...upstreamPayload(editForm),
      options: editOptions,
      status: editForm.status,
      expires_at: editForm.expires_at ? Math.floor(new Date(editForm.expires_at).getTime() / 1000) : null,
      fallback_mode: editForm.fallback_mode,
      backup_proxy_id: editForm.fallback_mode === 'proxy' ? editForm.backup_proxy_id : null,
      expiry_warn_days: editForm.expiry_warn_days,
      pool_id: editForm.pool_id, // 代理池（fork）
    }

    // Only include password if user actually modified the field
    if (editPasswordDirty.value) {
      updateData.password = editForm.password.trim()
    }

    await adminAPI.proxies.update(editingProxy.value.id, updateData)
    appStore.showSuccess(t('admin.proxies.proxyUpdated'))
    closeEditModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.failedToUpdate')))
    console.error('Error updating proxy:', error)
  } finally {
    submitting.value = false
  }
}

const applyLatencyResult = (
  proxyId: number,
  result: {
    success: boolean
    latency_ms?: number
    message?: string
    ip_address?: string
    country?: string
    country_code?: string
    region?: string
    city?: string
  }
) => {
  const target = proxies.value.find((proxy) => proxy.id === proxyId)
  if (!target) return
  if (result.success) {
    target.latency_status = 'success'
    target.latency_ms = result.latency_ms
    target.ip_address = result.ip_address
    target.country = result.country
    target.country_code = result.country_code
    target.region = result.region
    target.city = result.city
  } else {
    target.latency_status = 'failed'
    target.latency_ms = undefined
    target.ip_address = undefined
    target.country = undefined
    target.country_code = undefined
    target.region = undefined
    target.city = undefined
  }
  target.latency_message = result.message
}

interface QualityAISurfaceSpec {
  target: string
  platform: 'anthropic' | 'openai' | 'gemini' | 'grok'
  surface: 'api' | 'web' | 'code_assist'
  scopeKey: string
  defaultUrl: string
}

const QUALITY_AI_SURFACE_SPECS: QualityAISurfaceSpec[] = [
  {
    target: 'anthropic',
    platform: 'anthropic',
    surface: 'api',
    scopeKey: 'admin.proxies.qualityScopeAnthropicApi',
    defaultUrl: 'https://api.anthropic.com/v1/models'
  },
  {
    target: 'claude_web',
    platform: 'anthropic',
    surface: 'web',
    scopeKey: 'admin.proxies.qualityScopeClaudeWeb',
    defaultUrl: 'https://claude.ai/login'
  },
  {
    target: 'openai',
    platform: 'openai',
    surface: 'api',
    scopeKey: 'admin.proxies.qualityScopeOpenAIApi',
    defaultUrl: 'https://api.openai.com/v1/models'
  },
  {
    target: 'chatgpt',
    platform: 'openai',
    surface: 'web',
    scopeKey: 'admin.proxies.qualityScopeChatGPTWeb',
    defaultUrl: 'https://chatgpt.com/backend-api/models'
  },
  {
    target: 'gemini',
    platform: 'gemini',
    surface: 'api',
    scopeKey: 'admin.proxies.qualityScopeGeminiApi',
    defaultUrl: 'https://generativelanguage.googleapis.com/v1beta/models'
  },
  {
    target: 'gemini_code_assist',
    platform: 'gemini',
    surface: 'code_assist',
    scopeKey: 'admin.proxies.qualityScopeGeminiCodeAssist',
    defaultUrl: 'https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist'
  },
  {
    target: 'gemini_web',
    platform: 'gemini',
    surface: 'web',
    scopeKey: 'admin.proxies.qualityScopeGeminiWeb',
    defaultUrl: 'https://gemini.google.com/app'
  },
  {
    target: 'grok',
    platform: 'grok',
    surface: 'api',
    scopeKey: 'admin.proxies.qualityScopeGrokApi',
    defaultUrl: 'https://api.x.ai/v1/models'
  }
]

const QUALITY_AI_EXPECTED_TARGETS = QUALITY_AI_SURFACE_SPECS.map((s) => s.target)

const isReputationUnknown = (result?: ProxyQualityCheckResult | null): boolean => {
  if (!result) return true
  if (result.grade === 'Unknown') return true
  if (!result.reputation) return true
  if (result.reputation.status !== 'current') return true
  return typeof result.reputation.risk_score !== 'number'
}

const isRowQualityUnknown = (row?: Proxy | null): boolean => {
  if (!row) return true
  if (row.quality_grade === 'Unknown' || !row.quality_grade) return true
  return typeof row.quality_score !== 'number'
}

const hasAnyAIAssessments = (result?: ProxyQualityCheckResult | null): boolean => {
  return Boolean(result && Array.isArray(result.ai_platforms) && result.ai_platforms.length > 0)
}

const getQualityAITargetCounts = (result?: ProxyQualityCheckResult | null): { eligible: number; total: number } => {
  const total = Math.max(
    QUALITY_AI_EXPECTED_TARGETS.length,
    Array.isArray(result?.ai_platforms) ? result!.ai_platforms!.length : 0
  )
  if (!result || isReputationUnknown(result) || !result.base_eligible || !Array.isArray(result.ai_platforms)) {
    return { eligible: 0, total }
  }
  const eligible = result.ai_platforms.filter((a) => a.eligible && a.status === 'reachable').length
  return { eligible, total }
}

const summarizeQualityStatus = (result: ProxyQualityCheckResult): Proxy['quality_status'] => {
  if (isReputationUnknown(result)) return 'warn'
  if (!hasAnyAIAssessments(result)) {
    if (result.challenge_count > 0) return 'challenge'
    if (result.failed_count > 0) return 'failed'
    return 'warn'
  }
  const { eligible, total } = getQualityAITargetCounts(result)
  if (result.base_eligible && result.eligible && eligible > 0) {
    if (
      eligible < total ||
      result.challenge_count + result.failed_count + result.warn_count > 0
    ) {
      return 'warn'
    }
    return 'healthy'
  }
  if (result.challenge_count > 0) return 'challenge'
  if (result.failed_count > 0) return 'failed'
  if (result.warn_count > 0) return 'warn'
  return 'warn'
}

const applyQualityResult = (proxyId: number, result: ProxyQualityCheckResult) => {
  const target = proxies.value.find((proxy) => proxy.id === proxyId)
  if (!target) return
  const unknown = isReputationUnknown(result)
  target.quality_status = summarizeQualityStatus(result)
  target.quality_score = unknown ? undefined : result.score
  target.quality_grade = unknown ? 'Unknown' : result.grade
  target.quality_summary = result.summary
  target.quality_checked = result.checked_at
  target.quality_base_eligible = result.base_eligible
  target.quality_ai_platforms = result.ai_platforms
}
const formatLocation = (proxy: Proxy) => {
  const parts = [proxy.country, proxy.city].filter(Boolean) as string[]
  return parts.join(' · ')
}

const flagUrl = (code: string) =>
  `https://unpkg.com/flag-icons/flags/4x3/${code.toLowerCase()}.svg`

const startTestingProxy = (proxyId: number) => {
  testingProxyIds.value = new Set([...testingProxyIds.value, proxyId])
}

const stopTestingProxy = (proxyId: number) => {
  const next = new Set(testingProxyIds.value)
  next.delete(proxyId)
  testingProxyIds.value = next
}

const startQualityCheckingProxy = (proxyId: number) => {
  qualityCheckingProxyIds.value = new Set([...qualityCheckingProxyIds.value, proxyId])
}

const stopQualityCheckingProxy = (proxyId: number) => {
  const next = new Set(qualityCheckingProxyIds.value)
  next.delete(proxyId)
  qualityCheckingProxyIds.value = next
}

const runProxyTest = async (proxyId: number, notify: boolean) => {
  startTestingProxy(proxyId)
  try {
    const result = await adminAPI.proxies.testProxy(proxyId)
    applyLatencyResult(proxyId, result)
    if (notify) {
      if (result.success) {
        const message = result.latency_ms
          ? t('admin.proxies.proxyWorkingWithLatency', { latency: result.latency_ms })
          : t('admin.proxies.proxyWorking')
        appStore.showSuccess(message)
      } else {
        appStore.showError(result.message || t('admin.proxies.proxyTestFailed'))
      }
    }
    return result
  } catch (error: any) {
    const message = error.response?.data?.detail || t('admin.proxies.failedToTest')
    applyLatencyResult(proxyId, { success: false, message })
    if (notify) {
      appStore.showError(message)
    }
    console.error('Error testing proxy:', error)
    return null
  } finally {
    stopTestingProxy(proxyId)
  }
}

const handleTestConnection = async (proxy: Proxy) => {
  await runProxyTest(proxy.id, true)
}

const handleQualityCheck = async (proxy: Proxy) => {
  startQualityCheckingProxy(proxy.id)
  try {
    const result = await adminAPI.proxies.checkProxyQuality(proxy.id)
    qualityReportProxy.value = proxy
    qualityReport.value = result
    showQualityReportDialog.value = true

    const baseStep = result.items.find((item) => item.target === 'base_connectivity')
    if (baseStep && baseStep.status === 'pass') {
      applyLatencyResult(proxy.id, {
        success: true,
        latency_ms: result.base_latency_ms,
        message: result.summary,
        ip_address: result.exit_ip,
        country: result.country,
        country_code: result.country_code
      })
    }
    applyQualityResult(proxy.id, result)

    if (isReputationUnknown(result)) {
      appStore.showSuccess(
        t('admin.proxies.qualityCheckDoneUnknown', {
          reason: translateQualityReason(result.admission_reason || result.reputation?.reason) || t('admin.proxies.qualityReputationInsufficient')
        })
      )
    } else if (!hasAnyAIAssessments(result)) {
      appStore.showSuccess(
        t('admin.proxies.qualityCheckDoneLegacy', { score: result.score, grade: result.grade })
      )
    } else {
      const { eligible, total } = getQualityAITargetCounts(result)
      appStore.showSuccess(
        t('admin.proxies.qualityCheckDone', {
          score: result.score,
          grade: result.grade,
          eligible,
          total
        })
      )
    }
  } catch (error: any) {
    const message = error.response?.data?.detail || t('admin.proxies.qualityCheckFailed')
    appStore.showError(message)
    console.error('Error checking proxy quality:', error)
  } finally {
    stopQualityCheckingProxy(proxy.id)
  }
}

const runBatchProxyQualityChecks = async (ids: number[]) => {
  if (ids.length === 0) return { total: 0, healthy: 0, warn: 0, challenge: 0, failed: 0 }

  const concurrency = 3
  let index = 0
  let healthy = 0
  let warn = 0
  let challenge = 0
  let failed = 0

  const worker = async () => {
    while (index < ids.length) {
      const current = ids[index]
      index++
      startQualityCheckingProxy(current)
      try {
        const result = await adminAPI.proxies.checkProxyQuality(current)
        const target = proxies.value.find((proxy) => proxy.id === current)
        if (target) {
          const baseStep = result.items.find((item) => item.target === 'base_connectivity')
          if (baseStep && baseStep.status === 'pass') {
            applyLatencyResult(current, {
              success: true,
              latency_ms: result.base_latency_ms,
              message: result.summary,
              ip_address: result.exit_ip,
              country: result.country,
              country_code: result.country_code
            })
          }
        }
        applyQualityResult(current, result)
        const overallStatus = summarizeQualityStatus(result)
        if (overallStatus === 'challenge') {
          challenge++
        } else if (overallStatus === 'failed') {
          failed++
        } else if (overallStatus === 'warn') {
          warn++
        } else {
          healthy++
        }
      } catch {
        failed++
      } finally {
        stopQualityCheckingProxy(current)
      }
    }
  }

  const workers = Array.from({ length: Math.min(concurrency, ids.length) }, () => worker())
  await Promise.all(workers)
  return {
    total: ids.length,
    healthy,
    warn,
    challenge,
    failed
  }
}

const closeQualityReportDialog = () => {
  showQualityReportDialog.value = false
  qualityReportProxy.value = null
  qualityReport.value = null
}

const qualityStatusClass = (status: string) => {
  if (status === 'pass') return 'badge-success'
  if (status === 'warn') return 'badge-warning'
  if (status === 'challenge') return 'badge-danger'
  return 'badge-danger'
}

const qualityStatusLabel = (status: string) => {
  if (status === 'pass') return t('admin.proxies.qualityStatusPass')
  if (status === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (status === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

// 有效期「选天数」⇄ 日历联动:天数自 base 起算(创建=今天;编辑=代理创建日),本地日历日 round-trip 稳定;canonical 仍是 expires_at 日期串
const EXPIRY_PRESETS = [7, 30, 90, 180]
const toLocalDateStr = (dt: Date): string => {
  const y = dt.getFullYear()
  const m = String(dt.getMonth() + 1).padStart(2, '0')
  const d = String(dt.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}
// base 为空 → 今天本地 00:00;否则该日期本地 00:00
const baseDateOrToday = (baseDateStr: string): Date => {
  const base = baseDateStr ? new Date(`${baseDateStr}T00:00:00`) : new Date()
  base.setHours(0, 0, 0, 0)
  return base
}
// base + N 天 → 本地 YYYY-MM-DD;N≤0/空 → '' 表示永不过期
const addDaysToBase = (baseDateStr: string, n: number | null): string => {
  const days = Number(n)
  if (!days || days <= 0) return ''
  const dt = baseDateOrToday(baseDateStr)
  dt.setDate(dt.getDate() + days)
  return toLocalDateStr(dt)
}
// target 相对 base 的整天数(本地日历差,避免时区/时刻抖动)
const daysFromBase = (baseDateStr: string, targetDateStr: string): number | null => {
  if (!targetDateStr) return null
  const target = new Date(`${targetDateStr}T00:00:00`)
  return Math.round((target.getTime() - baseDateOrToday(baseDateStr).getTime()) / 86400000)
}
// 编辑时有效期自「代理创建日」起算;创建时无 created_at → base='' 用今天
const editBaseDate = computed(() =>
  editingProxy.value?.created_at ? editingProxy.value.created_at.slice(0, 10) : '',
)
const createExpiresDays = computed<number | null>({
  get: () => daysFromBase('', createForm.expires_at),
  set: (v) => {
    createForm.expires_at = addDaysToBase('', v)
  },
})
const editExpiresDays = computed<number | null>({
  get: () => daysFromBase(editBaseDate.value, editForm.expires_at),
  set: (v) => {
    editForm.expires_at = addDaysToBase(editBaseDate.value, v)
  },
})

const expiryLabel = (row: Proxy): string => {
  const { key, params } = proxyExpiryLabelKey(row.expires_at, row.status)
  return params ? t(key, params) : t(key)
}

const expiryBadgeClass = (row: Proxy): string =>
  proxyExpiryBadgeClass(row.expires_at, row.status)

const qualityInlineText = (row: Proxy) => {
  if (isRowQualityUnknown(row)) {
    return t('admin.proxies.qualityInlineUnknown')
  }
  return t('admin.proxies.qualityInline', {
    grade: row.quality_grade || '-',
    score: row.quality_score ?? '-'
  })
}

const resolveRowEffectiveQualityStatus = (status?: string, row?: Proxy): string | undefined => {
  if (!row) return status
  if (isRowQualityUnknown(row) && status === 'healthy') return 'warn'
  if (row.quality_base_eligible === false && status === 'healthy') return 'warn'
  if (Array.isArray(row.quality_ai_platforms)) {
    if (row.quality_ai_platforms.length === 0 && status === 'healthy') return 'warn'
    const allEligible =
      row.quality_ai_platforms.length >= QUALITY_AI_EXPECTED_TARGETS.length &&
      row.quality_ai_platforms.every((a) => a.eligible && a.status === 'reachable')
    if (!allEligible && status === 'healthy') return 'warn'
  }
  return status
}

const qualityOverallClass = (status?: string, row?: Proxy) => {
  const effectiveStatus = resolveRowEffectiveQualityStatus(status, row)
  if (effectiveStatus === 'healthy') return 'badge-success'
  if (effectiveStatus === 'warn') return 'badge-warning'
  if (effectiveStatus === 'challenge') return 'badge-danger'
  return 'badge-danger'
}

const qualityOverallLabel = (status?: string, row?: Proxy) => {
  const effectiveStatus = resolveRowEffectiveQualityStatus(status, row)
  if (effectiveStatus === 'healthy') return t('admin.proxies.qualityStatusHealthy')
  if (effectiveStatus === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (effectiveStatus === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

const qualityReportBaseClass = (report: ProxyQualityCheckResult) => {
  if (isReputationUnknown(report)) return 'badge-warning'
  if (report.base_eligible) return 'badge-success'
  return 'badge-warning'
}

const qualityReportBaseLabel = (report: ProxyQualityCheckResult) => {
  if (isReputationUnknown(report)) return t('admin.proxies.qualityBaseUnknown')
  if (report.base_eligible) return t('admin.proxies.qualityBaseQualified')
  return t('admin.proxies.qualityBaseBlocked')
}

const qualityReportEligibleClass = (report: ProxyQualityCheckResult) => {
  if (isReputationUnknown(report)) return 'badge-warning'
  if (!report.base_eligible) return 'badge-warning'
  if (!hasAnyAIAssessments(report)) return 'badge-warning'
  const { eligible, total } = getQualityAITargetCounts(report)
  if (eligible === 0 || !report.eligible) return 'badge-warning'
  if (eligible < total || report.challenge_count + report.failed_count + report.warn_count > 0) {
    return 'badge-warning'
  }
  return 'badge-success'
}

const qualityReportEligibleLabel = (report: ProxyQualityCheckResult) => {
  if (isReputationUnknown(report)) return t('admin.proxies.qualityEligibleInsufficient')
  if (!report.base_eligible) return t('admin.proxies.qualityEligibleBlocked')
  if (!hasAnyAIAssessments(report)) return t('admin.proxies.qualityEligibleMissingAI')
  const { eligible, total } = getQualityAITargetCounts(report)
  if (eligible === 0 || !report.eligible) {
    return t('admin.proxies.qualityEligibleBaseOnly', { eligible: 0, total })
  }
  if (eligible < total || report.challenge_count + report.failed_count + report.warn_count > 0) {
    return t('admin.proxies.qualityEligiblePartialAI', { eligible, total })
  }
  return t('admin.proxies.qualityEligibleAllowed', { eligible, total })
}

const formatUnixTimestamp = (sec?: number): string => {
  if (typeof sec !== 'number' || sec <= 0) return '-'
  return new Date(sec * 1000).toLocaleString()
}

const QUALITY_BACKEND_REASON_KEYS: Record<string, string> = {
  '已抵达该 AI 服务的鉴权边界；仅验证出口访问，登录与推理仍需有效账号验证': 'authBoundary',
  '观察到该 AI 的公开应用或登录页面；仅验证网页入口，未验证登录和对话': 'webReachable',
  '取得该 AI 服务的模型元数据响应；未验证真实推理请求': 'apiReachable',
  '该 AI 服务明确返回地区或国家限制；此结论只作用于该服务路径': 'geoRestricted',
  '该 AI 服务明确拒绝此 IP；其他 AI 服务单独判断': 'ipBlocked',
  '本次请求遇到访问挑战，尚未验证正常访问；挑战原因未归因到 IP 本身': 'wafChallenge',
  '本次请求受到频控；需复检，单次 429 不作为 IP 黑名单证据': 'rateLimited',
  '返回账号额度或计费限制，当前路径需复检；未据此判 IP 欺诈': 'quotaExhausted',
  '服务要求账号计费或组织访问条件；当前探测无法确认该账号可用，未据此判 IP 欺诈': 'accountRequirement',
  '上游服务异常或过载，当前可达性未确认；需复检': 'providerOverloaded',
  '该 AI 路径返回访问拒绝，具体原因尚未确认；不推断为 IP 欺诈': 'accessDenied',
  '响应不足以确认该 AI 服务的出口访问情况，需复检': 'unexpectedResponse',
  '请求被重定向到其他站点，未确认原 AI 服务路径；需复检': 'redirectedCrossSite',
  '收到未识别的错误响应，不能作为 AI 接口可达证据；需复检': 'invalidResponse',
  '代理出口连通正常': 'baseConnectivityOk',
  '代理探测服务未配置': 'proberNotConfigured',
  '静态落地代理 host 必须是出口 IP，域名无法确认固定出口': 'exitHostNotIP',
  '实际出口与登记落地 IP 不一致': 'exitIPMismatch',
  'IP 风险证据不足，暂不推荐优先分配': 'reputationInsufficient',
  'IP 风险达到高风险参考阈值 50，建议优先选择低风险 IP': 'reputationHighRisk',
  '基础出口或出口身份校验未通过，暂不推荐优先分配': 'baseCheckFailed',
  '基础 IP 检查通过；按账号实际使用的 AI 路径分别给出推荐': 'basePassScopedAdmission',
  '基础 IP 检查通过，AI 路径尚无推荐；可查看各服务证据并复检': 'basePassNoAITargets',
  '当前多源 IP 风险证据可用': 'reputationCurrentOk',
  '出口不是有效公网 IP': 'reputationNotPublicIP',
  '无法构建 IP 信誉检测请求': 'reputationReqBuildErr',
  'IPOK 请求失败或超时，证据不足': 'reputationTimeout',
  'IPOK 响应读取失败或超出大小限制': 'reputationReadErr',
  'IPOK 响应格式无效，证据不足': 'reputationInvalidJson',
  'IPOK 缺少当前多源风险证据': 'reputationMissingMultiSource',
  '信誉结果与探测出口 IP 不一致': 'reputationExitMismatch',
  'IPOK 评估时间缺失或过期': 'reputationAssessedAtStale',
  '数值风险来源缺少当前有效证据': 'reputationNumericSourceStale',
  '风险来源时间或评分无效，证据不足': 'reputationSourceTimeOrScoreInvalid',
  'DNSBL 黑名单检测未完整完成，证据不足': 'reputationDnsblIncomplete'
}

const QUALITY_EVIDENCE_REASON_KEYS: Record<string, string> = {
  authentication_required: 'authBoundary',
  web_reachable: 'webReachable',
  api_reachable: 'apiReachable',
  geo_restricted: 'geoRestricted',
  ip_blocked: 'ipBlocked',
  waf_challenge: 'wafChallenge',
  rate_limited: 'rateLimited',
  quota_exhausted: 'quotaExhausted',
  account_requirement: 'accountRequirement',
  provider_overloaded: 'providerOverloaded',
  access_denied: 'accessDenied',
  unexpected_response: 'unexpectedResponse',
  invalid_response: 'invalidResponse',
  network_error: 'networkError'
}

const translateQualityReason = (rawReason?: string, evidence?: string, status?: string): string => {
  const trimmed = rawReason?.trim() || ''
  if (trimmed && QUALITY_BACKEND_REASON_KEYS[trimmed]) {
    return t(`admin.proxies.qualityReasonMap.${QUALITY_BACKEND_REASON_KEYS[trimmed]}`)
  }
  const highRiskMatch = trimmed.match(/^IP 风险 (\d+)\/100，达到高风险参考阈值 50，建议优先选择低风险 IP；信号：(.*)$/)
  if (highRiskMatch) {
    const signals = highRiskMatch[2]?.trim()
    const base = `${t('admin.proxies.qualityRiskScoreDisplay', { score: highRiskMatch[1] })} · ${t('admin.proxies.qualityReasonMap.reputationHighRisk')}`
    return signals ? `${base} (${signals})` : base
  }
  const lowRiskMatch = trimmed.match(/^IP 风险 (\d+)\/100；(.*)$/)
  if (lowRiskMatch) {
    const tail = translateQualityReason(lowRiskMatch[2])
    return `${t('admin.proxies.qualityRiskScoreDisplay', { score: lowRiskMatch[1] })} · ${tail}`
  }
  const ipokHttpMatch = trimmed.match(/^IPOK 返回 HTTP (\d+)（可能额度不足），证据不足$/)
  if (ipokHttpMatch) {
    return `IPOK HTTP ${ipokHttpMatch[1]} (${t('admin.proxies.qualityReputationInsufficient')})`
  }
  if (status === 'missing') {
    return t('admin.proxies.qualityReasonMap.missingAssessment')
  }
  if (evidence && QUALITY_EVIDENCE_REASON_KEYS[evidence]) {
    const mapped = t(`admin.proxies.qualityReasonMap.${QUALITY_EVIDENCE_REASON_KEYS[evidence]}`)
    if (trimmed && evidence === 'network_error') {
      return `${mapped} (${trimmed})`
    }
    if (!trimmed) {
      return mapped
    }
  }
  return trimmed
}

const qualityReportAdmissionReason = (report: ProxyQualityCheckResult) => {
  if (report.admission_reason) return translateQualityReason(report.admission_reason)
  if (report.reputation?.reason) return translateQualityReason(report.reputation.reason)
  return t('admin.proxies.qualityReputationMissingReason')
}

const formatQualityReportSummary = (report: ProxyQualityCheckResult): string => {
  return t('admin.proxies.qualityCountsSummary', {
    passed: report.passed_count,
    warn: report.warn_count,
    challenge: report.challenge_count,
    failed: report.failed_count
  })
}

const qualitySurfaceLabel = (surface?: string): string => {
  switch (surface) {
    case 'api':
      return t('admin.proxies.qualitySurfaceApi')
    case 'web':
      return t('admin.proxies.qualitySurfaceWeb')
    case 'code_assist':
      return t('admin.proxies.qualitySurfaceCodeAssist')
    default:
      return t('admin.proxies.qualitySurfaceFoundation')
  }
}

const qualitySurfaceBadgeClass = (surface?: string): string => {
  switch (surface) {
    case 'api':
      return 'badge-primary'
    case 'web':
      return 'badge-gray'
    case 'code_assist':
      return 'badge-warning'
    default:
      return 'badge-gray'
  }
}

const qualityAIStatusLabel = (status?: string): string => {
  const key = status && ['reachable', 'restricted', 'challenge', 'rate_limited', 'account_limited', 'unavailable', 'unknown', 'missing'].includes(status)
    ? status
    : 'unknown'
  return t(`admin.proxies.qualityAIStatus.${key}`)
}

const qualityAIStatusBadgeClass = (status?: string, eligible?: boolean): string => {
  if (status === 'reachable') {
    return eligible ? 'badge-success' : 'badge-warning'
  }
  if (status === 'restricted' || status === 'challenge' || status === 'unavailable') {
    return 'badge-danger'
  }
  return 'badge-warning'
}

const qualityAIEvidenceLabel = (evidence?: string): string => {
  const known = [
    'authentication_required',
    'api_reachable',
    'web_reachable',
    'geo_restricted',
    'ip_blocked',
    'waf_challenge',
    'rate_limited',
    'quota_exhausted',
    'account_requirement',
    'provider_overloaded',
    'network_error',
    'access_denied',
    'unexpected_response',
    'invalid_response',
    'missing'
  ]
  if (evidence && known.includes(evidence)) {
    return t(`admin.proxies.qualityAIEvidence.${evidence}`)
  }
  return evidence || t('admin.proxies.qualityAIEvidence.missing')
}

const qualityAIEvidenceBadgeClass = (evidence?: string, eligible?: boolean): string => {
  if (evidence === 'authentication_required' || evidence === 'api_reachable' || evidence === 'web_reachable') {
    return eligible ? 'badge-success' : 'badge-warning'
  }
  if (evidence === 'geo_restricted' || evidence === 'ip_blocked' || evidence === 'waf_challenge' || evidence === 'access_denied' || evidence === 'network_error') {
    return 'badge-danger'
  }
  return 'badge-warning'
}

interface QualityAISurfaceRowView {
  target: string
  surface: string
  scopeHint: string
  status: string
  eligible: boolean
  candidateLabel: string
  candidateBadgeClass: string
  evidence: string
  reason: string
  errorCode: string
  httpStatus?: number
  latencyMs?: number
  cfRay: string
  retryAfter: string
  observedAt?: number
  url: string
  finalUrl: string
  isRedirected: boolean
  scopeCallout: string
  scopeCalloutClass: string
}

interface QualityAIProviderGroupView {
  platform: string
  label: string
  summaryText: string
  badgeClass: string
  rows: QualityAISurfaceRowView[]
}

const buildQualityAIProviderGroups = (report: ProxyQualityCheckResult): QualityAIProviderGroupView[] => {
  const assessmentsByTarget = new Map<string, ProxyAIPlatformAssessment>()
  for (const assessment of report.ai_platforms || []) {
    assessmentsByTarget.set(assessment.target, assessment)
  }
  const itemsByTarget = new Map<string, ProxyQualityCheckItem>()
  for (const item of report.items || []) {
    itemsByTarget.set(item.target, item)
  }

  const platformOrder: Array<{ platform: 'anthropic' | 'openai' | 'gemini' | 'grok'; labelKey: string }> = [
    { platform: 'anthropic', labelKey: 'admin.proxies.qualityPlatformAnthropic' },
    { platform: 'openai', labelKey: 'admin.proxies.qualityPlatformOpenAI' },
    { platform: 'gemini', labelKey: 'admin.proxies.qualityPlatformGemini' },
    { platform: 'grok', labelKey: 'admin.proxies.qualityPlatformGrok' }
  ]

  const repUnknown = isReputationUnknown(report)
  const baseOk = Boolean(report.base_eligible && !repUnknown)

  return platformOrder.map(({ platform, labelKey }) => {
    const specs = QUALITY_AI_SURFACE_SPECS.filter((s) => s.platform === platform)
    let eligibleCount = 0
    let assessedCount = 0
    let hardFailCount = 0

    const rows: QualityAISurfaceRowView[] = specs.map((spec) => {
      const assessment = assessmentsByTarget.get(spec.target)
      const item = itemsByTarget.get(spec.target)
      const hasAssessment = Boolean(assessment)
      if (hasAssessment) assessedCount++

      const status = assessment?.status || 'missing'
      const evidence = assessment?.evidence || item?.evidence || 'missing'
      const eligible = Boolean(baseOk && assessment?.eligible && status === 'reachable')
      if (eligible) eligibleCount++
      if (status === 'restricted' || status === 'challenge' || status === 'unavailable') {
        hardFailCount++
      }

      let candidateLabel = t('admin.proxies.qualityTargetCandidateNo')
      let candidateBadgeClass = 'badge-warning'
      if (!hasAssessment) {
        candidateLabel = t('admin.proxies.qualityTargetCandidateMissing')
        candidateBadgeClass = 'badge-warning'
      } else if (eligible) {
        candidateLabel = t('admin.proxies.qualityTargetCandidateYes')
        candidateBadgeClass = 'badge-success'
      } else if (status === 'restricted' || status === 'challenge' || status === 'unavailable') {
        candidateBadgeClass = 'badge-danger'
      }

      const url = item?.url || spec.defaultUrl
      const finalUrl = item?.final_url || ''
      const isRedirected = Boolean(finalUrl && url && finalUrl !== url)
      const reason = translateQualityReason(assessment?.reason || item?.message, evidence, status)

      let scopeCallout = ''
      let scopeCalloutClass = ''
      if (eligible) {
        scopeCallout = t('admin.proxies.qualityScopeCalloutBoundary')
        scopeCalloutClass = 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950/30 dark:text-emerald-300'
      } else if (
        status === 'rate_limited' ||
        status === 'account_limited' ||
        evidence === 'rate_limited' ||
        evidence === 'quota_exhausted' ||
        evidence === 'account_requirement' ||
        evidence === 'provider_overloaded'
      ) {
        scopeCallout = t('admin.proxies.qualityScopeCalloutTransient')
        scopeCalloutClass = 'bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300'
      } else if (status === 'restricted' || evidence === 'geo_restricted' || evidence === 'ip_blocked' || evidence === 'access_denied') {
        scopeCallout = t('admin.proxies.qualityScopeCalloutRestricted')
        scopeCalloutClass = 'bg-red-50 text-red-800 dark:bg-red-950/30 dark:text-red-300'
      }

      return {
        target: spec.target,
        surface: assessment?.surface || item?.surface || spec.surface,
        scopeHint: t(spec.scopeKey),
        status,
        eligible,
        candidateLabel,
        candidateBadgeClass,
        evidence,
        reason,
        errorCode: item?.error_code || '',
        httpStatus: item?.http_status,
        latencyMs: item?.latency_ms,
        cfRay: item?.cf_ray || '',
        retryAfter: item?.retry_after || '',
        observedAt: item?.observed_at,
        url,
        finalUrl,
        isRedirected,
        scopeCallout,
        scopeCalloutClass
      }
    })

    const total = rows.length
    let summaryText = t('admin.proxies.qualityPlatformSummaryCandidate', { eligible: eligibleCount, total })
    let badgeClass = 'badge-warning'
    if (assessedCount === 0) {
      summaryText = t('admin.proxies.qualityPlatformSummaryMissing', { total })
      badgeClass = 'badge-warning'
    } else if (eligibleCount === total && total > 0) {
      badgeClass = 'badge-success'
    } else if (eligibleCount === 0) {
      summaryText = t('admin.proxies.qualityPlatformSummaryNone', { total })
      badgeClass = hardFailCount > 0 ? 'badge-danger' : 'badge-warning'
    }

    return {
      platform,
      label: t(labelKey),
      summaryText,
      badgeClass,
      rows
    }
  })
}

const reputationSignalBadgeClass = (signal: string) => {
  const normalized = signal.toLowerCase()
  if (['tor', 'blacklist', 'proxy', 'vpn', 'abuser', 'abuse'].includes(normalized)) {
    return 'badge-danger'
  }
  return 'badge-warning'
}

const getSourceObservationState = (
  src: ProxyIPReputationSource,
  checkedAtSec?: number
): { state: 'current' | 'stale' | 'missing'; formatted?: string } => {
  if (!src.observed_at || !src.observed_at.trim()) {
    return { state: 'missing' }
  }
  const parsed = Date.parse(src.observed_at)
  if (Number.isNaN(parsed)) {
    return { state: 'missing' }
  }
  const formatted = new Date(parsed).toLocaleString()
  const refMs = checkedAtSec ? checkedAtSec * 1000 : Date.now()
  const ageMs = refMs - parsed
  if (ageMs >= 72 * 3600 * 1000 || ageMs < -60 * 1000) {
    return { state: 'stale', formatted }
  }
  return { state: 'current', formatted }
}

const formatSourceObservation = (src: ProxyIPReputationSource, checkedAtSec?: number) => {
  const info = getSourceObservationState(src, checkedAtSec)
  if (info.state === 'missing') {
    return t('admin.proxies.qualitySourceDateMissing')
  }
  if (info.state === 'stale') {
    return t('admin.proxies.qualitySourceDateStale', { time: info.formatted })
  }
  return t('admin.proxies.qualitySourceObservedAt', { time: info.formatted })
}

const sourceObservationClass = (src: ProxyIPReputationSource, checkedAtSec?: number) => {
  const info = getSourceObservationState(src, checkedAtSec)
  if (info.state !== 'current') {
    return 'text-amber-600 dark:text-amber-400'
  }
  return 'text-gray-500 dark:text-gray-400'
}

const sourceRiskBadgeClass = (src: ProxyIPReputationSource, checkedAtSec?: number) => {
  if (typeof src.risk_score !== 'number') return 'badge-gray'
  if (src.risk_score >= 50) return 'badge-danger'
  const obs = getSourceObservationState(src, checkedAtSec)
  if (obs.state !== 'current') return 'badge-warning'
  if (src.risk_score >= 25) return 'badge-warning'
  return 'badge-success'
}

const qualityTargetLabel = (target: string) => {
  switch (target) {
    case 'base_connectivity':
      return t('admin.proxies.qualityTargetBase')
    case 'ip_reputation':
      return t('admin.proxies.qualityTargetReputation')
    case 'exit_identity':
      return t('admin.proxies.qualityTargetExitIdentity')
    case 'http_client':
      return t('admin.proxies.qualityTargetHttpClient')
    case 'anthropic':
      return t('admin.proxies.qualityTargetAnthropic')
    case 'claude_web':
      return t('admin.proxies.qualityTargetClaudeWeb')
    case 'openai':
      return t('admin.proxies.qualityTargetOpenAI')
    case 'chatgpt':
      return t('admin.proxies.qualityTargetChatGPT')
    case 'gemini':
      return t('admin.proxies.qualityTargetGemini')
    case 'gemini_web':
      return t('admin.proxies.qualityTargetGeminiWeb')
    case 'gemini_code_assist':
      return t('admin.proxies.qualityTargetGeminiCodeAssist')
    case 'grok':
      return t('admin.proxies.qualityTargetGrok')
    case 'kimi':
      return 'Kimi'
    case 'zhipu':
      return 'Zhipu GLM'
    case 'deepseek':
      return 'DeepSeek'
    case 'minimax':
      return 'MiniMax'
    default:
      return target
  }
}

const fetchAllProxiesForBatch = async (): Promise<Proxy[]> => {
  const pageSize = 200
  const result: Proxy[] = []
  let page = 1
  let totalPages = 1

  while (page <= totalPages) {
    const response = await adminAPI.proxies.list(
      page,
      pageSize,
      {
        protocol: filters.protocol || undefined,
        status: filters.status as any,
        search: searchQuery.value || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      }
    )
    result.push(...response.items)
    totalPages = response.pages || 1
    page++
  }

  return result
}

const runBatchProxyTests = async (ids: number[]) => {
  if (ids.length === 0) return
  const concurrency = 5
  let index = 0

  const worker = async () => {
    while (index < ids.length) {
      const current = ids[index]
      index++
      await runProxyTest(current, false)
    }
  }

  const workers = Array.from({ length: Math.min(concurrency, ids.length) }, () => worker())
  await Promise.all(workers)
}

const handleBatchTest = async () => {
  if (batchTesting.value) return

  batchTesting.value = true
  try {
    let ids: number[] = []
    if (selectedCount.value > 0) {
      ids = Array.from(selectedProxyIds.value)
    } else {
      const allProxies = await fetchAllProxiesForBatch()
      ids = allProxies.map((proxy) => proxy.id)
    }

    if (ids.length === 0) {
      appStore.showInfo(t('admin.proxies.batchTestEmpty'))
      return
    }

    await runBatchProxyTests(ids)
    appStore.showSuccess(t('admin.proxies.batchTestDone', { count: ids.length }))
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchTestFailed'))
    console.error('Error batch testing proxies:', error)
  } finally {
    batchTesting.value = false
  }
}

const handleBatchQualityCheck = async () => {
  if (batchQualityChecking.value) return

  batchQualityChecking.value = true
  try {
    let ids: number[] = []
    if (selectedCount.value > 0) {
      ids = Array.from(selectedProxyIds.value)
    } else {
      const allProxies = await fetchAllProxiesForBatch()
      ids = allProxies.map((proxy) => proxy.id)
    }

    if (ids.length === 0) {
      appStore.showInfo(t('admin.proxies.batchQualityEmpty'))
      return
    }

    const summary = await runBatchProxyQualityChecks(ids)
    appStore.showSuccess(
      t('admin.proxies.batchQualityDone', {
        count: summary.total,
        healthy: summary.healthy,
        warn: summary.warn,
        challenge: summary.challenge,
        failed: summary.failed
      })
    )
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchQualityFailed'))
    console.error('Error batch checking quality:', error)
  } finally {
    batchQualityChecking.value = false
  }
}

const formatExportTimestamp = () => {
  const now = new Date()
  const pad2 = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad2(now.getMonth() + 1)}${pad2(now.getDate())}${pad2(now.getHours())}${pad2(now.getMinutes())}${pad2(now.getSeconds())}`
}

const handleExportData = async () => {
  if (exportingData.value) return
  exportingData.value = true
  try {
    const dataPayload = await adminAPI.proxies.exportData(
      selectedCount.value > 0
        ? { ids: Array.from(selectedProxyIds.value) }
        : {
            filters: buildProxyQueryFilters()
          }
    )
    const timestamp = formatExportTimestamp()
    const filename = `sub2api-proxy-${timestamp}.json`
    const blob = new Blob([JSON.stringify(dataPayload, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    link.click()
    URL.revokeObjectURL(url)
    appStore.showSuccess(t('admin.proxies.dataExported'))
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.proxies.dataExportFailed'))
  } finally {
    exportingData.value = false
    showExportDataDialog.value = false
  }
}

const handleDelete = (proxy: Proxy) => {
  if ((proxy.account_count || 0) > 0) {
    appStore.showError(t('admin.proxies.deleteBlockedInUse'))
    return
  }
  deletingProxy.value = proxy
  showDeleteDialog.value = true
}

const openBatchDelete = () => {
  if (selectedCount.value === 0) {
    return
  }
  showBatchDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingProxy.value) return

  try {
    await adminAPI.proxies.delete(deletingProxy.value.id)
    appStore.showSuccess(t('admin.proxies.proxyDeleted'))
    showDeleteDialog.value = false
    removeSelectedProxies([deletingProxy.value.id])
    deletingProxy.value = null
    loadProxies()
  } catch (error: any) {
    // 例如 PROXY_UPSTREAM_IN_USE：被其他代理作为前置代理引用或为默认前置代理
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.failedToDelete')))
    console.error('Error deleting proxy:', error)
  }
}

const confirmBatchDelete = async () => {
  const ids = Array.from(selectedProxyIds.value)
  if (ids.length === 0) {
    showBatchDeleteDialog.value = false
    return
  }

  try {
    const result = await adminAPI.proxies.batchDelete(ids)
    const deleted = result.deleted_ids?.length || 0
    const skipped = result.skipped?.length || 0

    if (deleted > 0) {
      appStore.showSuccess(t('admin.proxies.batchDeleteDone', { deleted, skipped }))
    } else if (skipped > 0) {
      appStore.showInfo(t('admin.proxies.batchDeleteSkipped', { skipped }))
    }

    clearSelectedProxies()
    showBatchDeleteDialog.value = false
    loadProxies()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.proxies.batchDeleteFailed')))
    console.error('Error batch deleting proxies:', error)
  }
}

const openAccountsModal = async (proxy: Proxy) => {
  accountsProxy.value = proxy
  proxyAccounts.value = []
  accountsLoading.value = true
  showAccountsModal.value = true

  try {
    proxyAccounts.value = await adminAPI.proxies.getProxyAccounts(proxy.id)
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.accountsFailed'))
    console.error('Error loading proxy accounts:', error)
  } finally {
    accountsLoading.value = false
  }
}

const closeAccountsModal = () => {
  showAccountsModal.value = false
  accountsProxy.value = null
  proxyAccounts.value = []
}

// ── Proxy URL copy ──
function buildAuthPart(row: any): string {
  const user = row.username ? encodeURIComponent(row.username) : ''
  const pass = row.password ? encodeURIComponent(row.password) : ''
  if (user && pass) return `${user}:${pass}@`
  if (user) return `${user}@`
  if (pass) return `:${pass}@`
  return ''
}

function buildProxyUrl(row: any): string {
  return `${row.protocol}://${buildAuthPart(row)}${row.host}:${row.port}`
}

function getCopyFormats(row: any) {
  const hasAuth = row.username || row.password
  const fullUrl = buildProxyUrl(row)
  const formats = [
    { label: fullUrl, value: fullUrl },
  ]
  if (hasAuth) {
    const withoutProtocol = fullUrl.replace(/^[^:]+:\/\//, '')
    formats.push({ label: withoutProtocol, value: withoutProtocol })
  }
  formats.push({ label: `${row.host}:${row.port}`, value: `${row.host}:${row.port}` })
  return formats
}

function copyProxyUrl(row: any) {
  copyToClipboard(buildProxyUrl(row), t('admin.proxies.urlCopied'))
  copyMenuProxyId.value = null
}

function toggleCopyMenu(id: number) {
  copyMenuProxyId.value = copyMenuProxyId.value === id ? null : id
}

function copyFormat(value: string) {
  copyToClipboard(value, t('admin.proxies.urlCopied'))
  copyMenuProxyId.value = null
}

function closeCopyMenu() {
  copyMenuProxyId.value = null
}

onMounted(() => {
  loadProxies()
  loadBackupProxyOptions()
  document.addEventListener('click', closeCopyMenu)
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
  document.removeEventListener('click', closeCopyMenu)
})
</script>
