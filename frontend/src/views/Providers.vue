<template>
  <div class="space-y-5">
    <!-- 顶栏：搜索 + 计数 / 视图切换 + 刷新 + 新增 -->
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <div class="relative">
          <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            v-model.trim="search"
            class="input !w-full !py-2 !pl-9 text-sm sm:!w-80"
            :placeholder="t('provider.searchPlaceholder')"
          />
        </div>
        <p class="mt-1.5 text-xs text-gray-500 dark:text-dark-400">
          {{ t('provider.summary', { connected: connectedCount, total: providers.length }) }}
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <!-- 视图切换 -->
        <div class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800" role="group">
          <button
            type="button" class="rounded-md px-2.5 py-1.5 transition-colors"
            :class="viewMode === 'list' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
            :aria-pressed="viewMode === 'list'" :title="t('provider.viewList')"
            @click="viewMode = 'list'"
          ><Icon name="menu" size="sm" /></button>
          <button
            type="button" class="rounded-md px-2.5 py-1.5 transition-colors"
            :class="viewMode === 'card' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
            :aria-pressed="viewMode === 'card'" :title="t('provider.viewCard')"
            @click="viewMode = 'card'"
          ><Icon name="grid" size="sm" /></button>
        </div>

        <!-- 自动刷新状态 -->
        <span class="hidden items-center rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-xs text-gray-500 md:inline-flex dark:border-dark-700 dark:bg-dark-800 dark:text-dark-400">
          {{ refreshHint }}
        </span>

        <button class="btn btn-secondary text-sm" @click="load" :disabled="loading">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          {{ t('provider.refreshData') }}
        </button>
        <button
          class="btn btn-secondary text-sm"
          @click="openRefreshAll"
          :disabled="refreshingAll || !collectableCount"
          :title="t('provider.refreshAll')"
        >
          <Icon name="refresh" size="sm" :class="refreshingAll ? 'animate-spin' : ''" />
          {{ refreshingAll ? t('provider.refreshAllRunning') : t('provider.refreshAll') }}
        </button>
        <button class="btn btn-secondary text-sm" @click="openScan" :disabled="scanning">
          <Icon name="search" size="sm" />
          {{ scanning ? t('common.loading') : t('provider.scan') }}
        </button>
        <button class="btn btn-primary text-sm" @click="openCreate">
          <Icon name="plus" size="sm" />
          {{ t('provider.addSite') }}
        </button>
      </div>
    </div>

    <!-- 筛选与排序 -->
    <ProviderToolbar
      :platform-opts="platformOpts" :status-opts="statusOpts" :balance-type-opts="balanceTypeOpts"
      v-model:platform="platformFilter" v-model:status="statusFilter"
      v-model:balance-type="balanceTypeFilter" v-model:sort-key="sortKey"
    />

    <!-- 卡片视图 -->
    <div v-if="viewMode === 'card'">
      <LoadingState v-if="loading && !providers.length" />
      <EmptyState v-else-if="!filteredProviders.length" icon="server" />
      <div v-else class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3">
        <ProviderCard
          v-for="(p, i) in filteredProviders" :key="p.id"
          :provider="p" :index="i" :refreshing="refreshingId === p.id"
          :default-balance-threshold="defaultBalanceThreshold"
          @refresh="refreshBalance" @settings="openSiteSettings" @edit="openEdit"
          @delete="onDelete" @groups="openGroups" @link="openLinks"
          @opcost="openOpCosts"
        />
      </div>
    </div>

    <!-- 列表视图 -->
    <div v-else class="card overflow-hidden">
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('provider.name') }}</th>
              <th>{{ t('provider.balanceType') }}</th>
              <th>{{ t('provider.lastBalance') }}</th>
              <th>{{ t('provider.todayCost') }}</th>
              <th>{{ t('provider.accountCount') }}</th>
              <th>{{ t('provider.probeEnabled') }}</th>
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState :loading="loading" :empty="!filteredProviders.length" :colspan="7" icon="server" />
            <tr
              v-for="p in filteredProviders"
              :key="p.id"
              :class="isLowBalance(p, defaultBalanceThreshold) ? 'bg-red-50/70 dark:bg-red-900/10' : ''"
            >
              <td>
                <div class="flex items-center gap-1.5">
                  <!-- 采集健康点：绿=正常 黄=1-2 次失败 红=3+ 或冷却 灰=未采集 -->
                  <span
                    v-if="p.balance_type === 'sub2api'"
                    class="inline-block h-2 w-2 flex-shrink-0 rounded-full"
                    :class="healthDotClass(p)"
                    :title="healthTitle(p)"
                  ></span>
                  <span v-if="isLowBalance(p, defaultBalanceThreshold)" class="text-red-500" :title="t('provider.lowBalanceAlert')">
                    <Icon name="exclamationTriangle" size="xs" />
                  </span>
                  <span class="font-medium text-gray-900 dark:text-white">{{ p.name }}</span>
                  <span v-if="p.platform === 'new-api'" class="badge badge-purple !text-[10px]">new-api</span>
                  <span
                    v-if="p.self_operated"
                    class="badge badge-success !text-[10px]"
                    :title="t('provider.selfOperatedHint')"
                  >{{ t('provider.selfOperated') }}</span>
                </div>
                <div v-if="p.note" class="text-xs text-gray-400">{{ p.note }}</div>
                <div v-if="p.login_cooldown_until" class="text-xs text-amber-600 dark:text-amber-400">
                  {{ t('provider.loginCooldown', { time: p.login_cooldown_until }) }}
                </div>
              </td>
              <td>
                <Badge :variant="balanceTypeVariant(p.balance_type)">
                  {{ balanceTypeLabel(p.balance_type) }}
                </Badge>
                <div v-if="p.balance_type === 'sub2api' && p.last_balance_error" class="mt-0.5 max-w-[180px] truncate text-xs text-red-500" :title="p.last_balance_error">⚠ {{ p.last_balance_error }}</div>
              </td>
              <td>
                <span v-if="p.last_balance !== null && p.last_balance !== undefined" :class="isLowBalance(p, defaultBalanceThreshold) ? 'font-semibold text-red-600' : 'font-semibold text-gray-900 dark:text-white'">
                  {{ fmtMoney(p.last_balance) }}
                </span>
                <span v-else class="text-gray-400">-</span>
                <div v-if="p.last_balance_at" class="text-xs text-gray-400">{{ p.last_balance_at }}</div>
              </td>
              <td>
                <span v-if="p.today_cost !== null && p.today_cost !== undefined" class="text-gray-900 dark:text-white">
                  {{ fmtMoney(p.today_cost) }}
                </span>
                <span v-else class="text-gray-400">-</span>
              </td>
              <td>
                <button class="text-primary-600 hover:underline dark:text-primary-400" @click="openAccounts(p)">{{ p.account_count }}</button>
              </td>
              <td>
                <span :class="p.probe_enabled ? 'text-emerald-600' : 'text-gray-400'">{{ p.probe_enabled ? '✓' : '—' }}</span>
              </td>
              <td>
                <div class="flex items-center gap-1">
                  <button
                    v-if="p.balance_type === 'sub2api'"
                    @click="refreshBalance(p)"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-emerald-50 hover:text-emerald-600 dark:hover:bg-emerald-900/20 dark:hover:text-emerald-400"
                  >
                    <Icon name="refresh" size="sm" />
                    <span class="text-xs">{{ t('provider.refresh') }}</span>
                  </button>
                  <button
                    v-if="p.balance_type === 'manual'"
                    @click="openManual(p)"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20 dark:hover:text-blue-400"
                  >
                    <Icon name="calculator" size="sm" />
                    <span class="text-xs">{{ t('provider.manualSet') }}</span>
                  </button>
                  <button
                    v-if="p.balance_type !== 'none'"
                    @click="openHistory(p)"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-600 dark:hover:text-gray-300"
                  >
                    <Icon name="clock" size="sm" />
                    <span class="text-xs">{{ t('provider.history') }}</span>
                  </button>
                  <button
                    v-if="p.balance_type === 'sub2api'"
                    @click="openCosts(p)"
                    :title="t('cost.keyDetail')"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-orange-50 hover:text-orange-600 dark:hover:bg-orange-900/20 dark:hover:text-orange-400"
                  >
                    <Icon name="dollar" size="sm" />
                    <span class="text-xs">{{ t('stats.cost') }}</span>
                  </button>
                  <!-- 运营成本仅自营站可录：非自营站的成本已由上游实扣完整表达 -->
                  <button
                    v-if="p.self_operated"
                    @click="openOpCosts(p)"
                    :title="t('opcost.title')"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-teal-50 hover:text-teal-600 dark:hover:bg-teal-900/20 dark:hover:text-teal-400"
                  >
                    <Icon name="creditCard" size="sm" />
                    <span class="text-xs">{{ t('opcost.short') }}</span>
                  </button>
                  <button
                    @click="openAccounts(p)"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-purple-600 dark:hover:bg-dark-700 dark:hover:text-purple-400"
                  >
                    <Icon name="users" size="sm" />
                    <span class="text-xs">{{ t('provider.accounts') }}</span>
                  </button>
                  <button
                    @click="openLinks(p)"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-emerald-600 dark:hover:bg-dark-700 dark:hover:text-emerald-400"
                  >
                    <Icon name="link" size="sm" />
                    <span class="text-xs">{{ t('provider.link') }}</span>
                  </button>
                  <button
                    @click="openEdit(p)"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                  >
                    <Icon name="edit" size="sm" />
                    <span class="text-xs">{{ t('common.edit') }}</span>
                  </button>
                  <button
                    @click="onDelete(p)"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                  >
                    <Icon name="trash" size="sm" />
                    <span class="text-xs">{{ t('common.delete') }}</span>
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 新建/编辑弹窗 -->
    <BaseDialog :show="showForm" :title="editing ? t('provider.editTitle') : t('provider.createTitle')" @close="showForm = false">
      <form id="provider-form" @submit.prevent="saveForm" class="space-y-4">
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('provider.name') }}</label>
            <input v-model.trim="form.name" class="input" required :disabled="!!editing" />
          </div>
          <div>
            <label class="input-label">{{ t('provider.note') }}</label>
            <input v-model.trim="form.note" class="input" />
          </div>
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('provider.balanceType') }}</label>
            <Select v-model="form.balance_type" :options="formBalanceTypeOptions" :searchable="false" />
          </div>
          <div>
            <label class="input-label">{{ t('provider.lowBalance') }}</label>
            <input v-model.number="form.low_balance_threshold" type="number" step="0.01" min="0" class="input" />
          </div>
        </div>

        <template v-if="form.balance_type === 'sub2api'">
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="input-label">{{ t('provider.platform') }}</label>
              <Select
                v-model="form.platform"
                :options="formPlatformOptions"
                :searchable="false"
                @change="onPlatformChange"
              />
            </div>
            <div>
              <label class="input-label">{{ t('provider.authMode') }}</label>
              <Select v-model="form.auth_mode" :options="formAuthModeOptions" :searchable="false" />
            </div>
          </div>
          <div>
            <label class="input-label">{{ t('provider.baseUrl') }}</label>
            <input v-model.trim="form.base_url" class="input" placeholder="https://example.com" />
          </div>

          <!-- password：邮箱/账号 + 密码 -->
          <div v-if="form.auth_mode === 'password'" class="grid grid-cols-2 gap-4">
            <div>
              <label class="input-label">{{ form.platform === 'new-api' ? t('provider.loginUsername') : t('provider.loginEmail') }}</label>
              <input v-model.trim="form.login_email" class="input" autocomplete="off" />
            </div>
            <div>
              <label class="input-label">{{ t('provider.loginPassword') }}</label>
              <input v-model="form.login_password" type="password" class="input" :placeholder="editing ? t('provider.passwordPlaceholder') : ''" autocomplete="new-password" />
            </div>
          </div>

          <!-- sub2api token：Access + Refresh Token -->
          <div v-else-if="form.auth_mode === 'token'" class="space-y-3">
            <div>
              <label class="input-label">Access Token</label>
              <input v-model.trim="form.access_token" type="password" class="input"
                :placeholder="editing?.has_access_token ? t('provider.passwordPlaceholder') : ''" autocomplete="new-password" />
            </div>
            <div>
              <label class="input-label">Refresh Token</label>
              <input v-model.trim="form.refresh_token" type="password" class="input"
                :placeholder="editing?.has_refresh_token ? t('provider.passwordPlaceholder') : t('provider.refreshTokenHint')" autocomplete="new-password" />
            </div>
          </div>

          <!-- new-api user_key：系统访问令牌 + 用户 ID -->
          <div v-else class="grid grid-cols-2 gap-4">
            <div>
              <label class="input-label">{{ t('provider.systemToken') }}</label>
              <input v-model.trim="form.access_token" type="password" class="input"
                :placeholder="editing?.has_access_token ? t('provider.passwordPlaceholder') : ''" autocomplete="new-password" />
            </div>
            <div>
              <label class="input-label">{{ t('provider.upstreamUserId') }}</label>
              <input v-model.trim="form.upstream_user_id" class="input" placeholder="1" />
            </div>
          </div>

          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-secondary text-xs" @click="testConn" :disabled="testing">
              {{ testing ? t('common.loading') : t('provider.testConnection') }}
            </button>
            <span v-if="testResult" class="text-xs" :class="testOk ? 'text-emerald-600' : 'text-red-500'">{{ testResult }}</span>
          </div>
        </template>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('provider.rechargeRate') }}</label>
            <input v-model.number="form.recharge_rate" type="number" step="0.01" min="0.01" class="input" />
            <p class="mt-1 text-xs text-gray-400">{{ t('provider.rechargeRateHint') }}</p>
          </div>
        </div>

        <div class="flex items-center gap-6 rounded-lg bg-gray-50 p-3 dark:bg-dark-800/50">
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.probe_enabled" type="checkbox" class="checkbox" />
            {{ t('provider.probeEnabled') }}
          </label>
          <div v-if="form.probe_enabled" class="flex-1">
            <input v-model.trim="form.probe_model" class="input !py-1.5 text-xs" :placeholder="t('provider.probeModel')" />
          </div>
        </div>

        <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">{{ formError }}</p>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showForm = false">{{ t('common.cancel') }}</button>
        <button type="submit" form="provider-form" class="btn btn-primary" :disabled="saving">{{ saving ? t('common.loading') : t('common.save') }}</button>
      </template>
    </BaseDialog>

    <!-- 扫描导入弹窗：两种发现方式并列 -->
    <BaseDialog :show="showScan" :title="t('provider.scanTitle')" width="wide" @close="showScan = false">
      <div class="space-y-4">
        <!-- 页签：按【】前缀（依赖命名习惯）/ 按站点地址（只看账号实际连哪） -->
        <div class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
          <button
            v-for="tabKey in (['prefix', 'url'] as const)" :key="tabKey"
            type="button"
            class="flex-1 rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
            :class="scanTab === tabKey
              ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
              : 'text-gray-500 hover:text-gray-700 dark:text-dark-400'"
            @click="switchScanTab(tabKey)"
          >
            {{ t(tabKey === 'prefix' ? 'provider.scanByPrefix' : 'provider.scanByUrl') }}
          </button>
        </div>

        <!-- 快捷导入：按【】前缀批量建站，逐行可改地址，顺带关联该前缀下的账号 -->
        <template v-if="scanTab === 'prefix'">
          <p class="text-xs text-gray-400">{{ t('provider.scanPrefixHint') }}</p>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <label class="flex items-center gap-2 text-sm">
              <input type="checkbox" class="checkbox" :checked="allSelected" @change="toggleAll" />
              {{ t('provider.selectAll') }}
            </label>
            <div class="flex items-center gap-2">
              <!-- 采集方式：默认 API 采集。缺凭据的站点不会真去采（后端凭据门禁），
                   建完站再去编辑页补账密即可开始采集 -->
              <Select
                v-model="importBalanceType"
                :options="formBalanceTypeOptions"
                :searchable="false"
                class="!w-36"
              />
              <button class="btn btn-primary text-sm" :disabled="!selectedNames.length || importing" @click="doImport">
                {{ importing ? t('common.loading') : t('provider.importSelected') + ' (' + selectedNames.length + ')' }}
              </button>
            </div>
          </div>
          <p v-if="importBalanceType === 'sub2api'" class="text-xs text-amber-600 dark:text-amber-400">
            {{ t('provider.importCredHint') }}
          </p>
          <LoadingState v-if="scanning" />
          <EmptyState v-else-if="!scanItems.length" icon="search" />
          <div v-else class="max-h-96 space-y-1 overflow-y-auto">
            <div
              v-for="item in scanItems" :key="item.prefix"
              class="rounded-lg border px-3 py-2 text-sm transition-colors"
              :class="item.exists ? 'border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800/50' : 'border-gray-200 dark:border-dark-700'"
            >
              <div class="flex flex-wrap items-center justify-between gap-2">
                <label class="flex min-w-0 cursor-pointer items-center gap-2">
                  <!-- 已建站的仍可勾选：老站点后来新增的账号也要能从这里补关联 -->
                  <input type="checkbox" :value="item.prefix" v-model="selectedNames" class="checkbox" />
                  <span class="truncate font-medium">{{ item.prefix }}</span>
                  <span v-if="item.exists" class="badge badge-success shrink-0 !text-[10px]">
                    {{ t('provider.alreadyCreatedShort') }}
                  </span>
                </label>
                <div class="flex items-center gap-2">
                  <input
                    v-model.trim="scanUrls[item.prefix]"
                    class="input !w-56 !py-1.5 text-xs"
                    :placeholder="t('provider.baseUrl')"
                  />
                  <span class="shrink-0 text-xs text-gray-500">
                    {{ item.account_count }} {{ t('provider.accountCount') }}
                  </span>
                </div>
              </div>
              <!-- 同一前缀下的账号连着多个地址：预填值是任取的，必须让人核对 -->
              <p v-if="item.url_count > 1" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
                {{ t('provider.multiUrlHint', { n: item.url_count }) }}
              </p>
            </div>
          </div>
        </template>

        <!-- 按站点地址：不依赖命名习惯，看账号实际连的是哪个上游 -->
        <template v-else>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <p class="text-xs text-gray-400">{{ t('provider.scanUrlHint') }}</p>
            <Select
              v-model="importBalanceType"
              :options="formBalanceTypeOptions"
              :searchable="false"
              class="!w-36"
            />
          </div>
          <LoadingState v-if="urlScanning" />
          <EmptyState v-else-if="!urlGroups.length" icon="search" />
          <div v-else class="max-h-96 space-y-3 overflow-y-auto">
            <div
              v-for="g in urlGroups" :key="g.base_url"
              class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"
            >
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div class="min-w-0">
                  <p class="truncate font-mono text-sm text-gray-900 dark:text-white">
                    {{ g.base_url || t('provider.noBaseUrl') }}
                  </p>
                  <p class="truncate text-xs text-gray-400">
                    {{ g.account_count }} {{ t('provider.accountCount') }}
                    <span v-if="g.existing_provider" class="ml-1 text-emerald-600 dark:text-emerald-400">
                      · {{ t('provider.alreadyCreated', { name: g.existing_provider }) }}
                    </span>
                  </p>
                </div>
                <div class="flex items-center gap-2">
                  <input
                    v-model.trim="urlGroupNames[g.base_url]"
                    class="input !w-40 !py-1.5 text-sm"
                    :placeholder="t('provider.providerName')"
                  />
                  <button
                    class="btn btn-primary text-sm"
                    :disabled="importing || !urlSelected[g.base_url]?.length || !urlGroupNames[g.base_url]"
                    @click="importUrlGroup(g)"
                  >
                    {{ t('provider.linkSelected') }} ({{ urlSelected[g.base_url]?.length || 0 }})
                  </button>
                </div>
              </div>

              <label class="mt-2 flex items-center gap-2 text-xs text-gray-500">
                <input
                  type="checkbox" class="checkbox"
                  :checked="isUrlGroupAllSelected(g)"
                  @change="toggleUrlGroup(g)"
                />
                {{ t('provider.selectAll') }}
              </label>

              <div class="mt-1 max-h-40 space-y-1 overflow-y-auto">
                <label
                  v-for="a in g.accounts" :key="a.id"
                  class="flex cursor-pointer items-center justify-between gap-2 rounded px-2 py-1 text-sm hover:bg-gray-50 dark:hover:bg-dark-800"
                >
                  <span class="flex min-w-0 items-center gap-2">
                    <input type="checkbox" :value="a.id" v-model="urlSelected[g.base_url]" class="checkbox" />
                    <span class="truncate">{{ a.name }}</span>
                  </span>
                  <span class="flex shrink-0 items-center gap-1.5 text-xs text-gray-400">
                    {{ a.platform }}
                    <!-- 勾选已归属别的站的账号 = 把它抢过来，必须让人看见 -->
                    <span v-if="a.linked_to" class="badge badge-warning !text-[10px]">
                      {{ t('provider.linkedTo', { name: a.linked_to }) }}
                    </span>
                  </span>
                </label>
              </div>
            </div>
          </div>
        </template>
      </div>
    </BaseDialog>

    <!-- 关联账号弹窗（供应商行内入口）：全量替换该站的关联集合 -->
    <ProviderLinkDialog
      :show="showLinks"
      :provider="linkProvider"
      @close="showLinks = false"
      @saved="load"
    />

    <!-- 运营成本弹窗（仅自营站入口可见）：买号/订阅/服务器等站外支出 -->
    <OperatingCostDialog
      :show="showOpCosts"
      :provider="opCostProvider"
      @close="showOpCosts = false"
    />


    <!-- 子账号弹窗 -->
    <BaseDialog :show="showAccounts" :title="currentProvider?.name + ' · ' + t('provider.accounts')" width="wide" @close="showAccounts = false">
      <LoadingState v-if="accountsLoading" />
      <EmptyState v-else-if="!accounts.length" icon="users" />
      <div v-else class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>ID</th><th>{{ t('provider.name') }}</th><th>{{ t('common.platform') }}</th>
              <th>{{ t('common.status') }}</th><th>{{ t('stats.rateMultiplier') }}</th><th>{{ t('stats.groupName') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in accounts" :key="a.id">
              <td class="font-mono text-xs">{{ a.id }}</td>
              <td>{{ a.name }}</td>
              <td><span class="rounded bg-gray-100 px-1.5 py-0.5 text-xs dark:bg-dark-800">{{ a.platform }}</span></td>
              <td><span :class="a.status === 'active' ? 'text-emerald-600' : 'text-red-500'">{{ a.status }}</span></td>
              <td>{{ a.rate_multiplier }}</td>
              <td class="text-xs text-gray-500">{{ (a.groups || []).join(', ') || '-' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </BaseDialog>

    <!-- 手动录入余额 -->
    <BaseDialog :show="showManual" :title="currentProvider?.name + ' · ' + t('provider.manualSet')" @close="showManual = false">
      <form id="manual-form" @submit.prevent="saveManual" class="space-y-4">
        <div>
          <label class="input-label">{{ t('provider.lastBalance') }}</label>
          <input v-model.number="manualBalance" type="number" step="0.01" class="input" required autofocus />
        </div>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showManual = false">{{ t('common.cancel') }}</button>
        <button type="submit" form="manual-form" class="btn btn-primary" :disabled="saving">{{ t('common.save') }}</button>
      </template>
    </BaseDialog>

    <!-- 余额历史 -->
    <BaseDialog :show="showHistory" :title="currentProvider?.name + ' · ' + t('provider.history')" width="extra-wide" @close="showHistory = false">
      <LoadingState v-if="historyLoading" />
      <div v-else>
        <div v-if="historyItems.length" class="mb-4">
          <LineChart :labels="historyLabels" :datasets="historyDatasets" :height="220" />
        </div>
        <div class="table-wrapper">
          <table class="table">
            <thead>
              <tr>
                <th>{{ t('common.date') }}</th><th>{{ t('provider.lastBalance') }}</th>
                <th>{{ t('dash.cost') }}</th><th>{{ t('dash.requests') }}</th>
                <th>RPM/TPM</th><th>{{ t('stability.source') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="h in historyItems" :key="h.id">
                <td class="text-xs">{{ h.created_at }}</td>
                <td class="font-semibold">{{ fmtMoney(h.balance) }}</td>
                <td>{{ fmtMoney(h.today_cost) }}</td>
                <td>{{ fmtNum(h.today_requests) }}</td>
                <td class="text-xs">{{ h.rpm ?? '-' }} / {{ h.tpm ?? '-' }}</td>
                <td>
                  <span class="text-xs text-gray-500">{{ h.source }}</span>
                  <div v-if="h.error" class="max-w-[160px] truncate text-xs text-red-500" :title="h.error">{{ h.error }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <Pagination :page="historyPage" :pages="historyPages" :total="historyTotal" @change="loadHistory" />
      </div>
    </BaseDialog>

    <!-- 上游 per-key 成本明细 -->
    <BaseDialog :show="showCosts" :title="currentProvider?.name + ' · ' + t('cost.keyDetail')" width="extra-wide" @close="showCosts = false">
      <div class="space-y-4">
        <!-- 区间与同步操作 -->
        <div class="flex flex-wrap items-center gap-2">
          <input v-model="costStart" type="date" class="input !w-auto !py-1.5 text-sm" @change="loadCosts" />
          <span class="text-gray-400">~</span>
          <input v-model="costEnd" type="date" class="input !w-auto !py-1.5 text-sm" @change="loadCosts" />
          <div class="ml-auto flex gap-2">
            <button class="btn btn-secondary text-xs" :disabled="syncing" @click="doSync(false)">
              {{ syncing ? t('common.loading') : t('cost.syncNow') }}
            </button>
            <button class="btn btn-secondary text-xs" :disabled="syncing" :title="t('cost.backfillHint')" @click="doSync(true)">
              {{ t('cost.backfill') }}
            </button>
          </div>
        </div>

        <p v-if="costError" class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-600 dark:bg-red-900/30 dark:text-red-400">{{ costError }}</p>

        <!-- 同步状态 -->
        <div v-if="costs" class="flex flex-wrap gap-x-4 gap-y-1 rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:bg-dark-800/50 dark:text-dark-400">
          <span>{{ t('cost.syncedAt') }} {{ fmtDateTime(costs.sync_state.last_synced_at) }}</span>
          <span>{{ t('cost.keysMatched', { matched: costs.sync_state.keys_matched, total: costs.sync_state.keys_total }) }}</span>
          <span v-if="costs.sync_state.backfilled_at">{{ t('cost.backfilledAt') }} {{ fmtDateTime(costs.sync_state.backfilled_at) }}</span>
          <span v-if="costs.sync_state.last_error" class="max-w-[300px] truncate text-red-600 dark:text-red-400" :title="costs.sync_state.last_error">
            {{ costs.sync_state.last_error }}
          </span>
        </div>

        <!-- 合计 -->
        <div v-if="costs" class="grid grid-cols-2 gap-3">
          <div class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
            <p class="text-xs text-gray-500">{{ t('cost.actual') }}</p>
            <p class="text-lg font-bold text-gray-900 dark:text-white">{{ fmtMoney(costs.actual_cost) }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-700">
            <p class="text-xs text-gray-500">{{ t('cost.official') }}</p>
            <p class="text-lg font-bold text-gray-400">{{ fmtMoney(costs.official_cost) }}</p>
          </div>
        </div>

        <LoadingState v-if="costsLoading" />
        <EmptyState v-else-if="!costItems.length" icon="dollar" :title="t('cost.noData')" />
        <div v-else class="table-wrapper">
          <table class="table">
            <thead>
              <tr>
                <th>{{ t('cost.keyName') }}</th>
                <th>{{ t('cost.mappedAccount') }}</th>
                <th>{{ t('stats.rateMultiplier') }}</th>
                <th class="text-right">{{ t('cost.actual') }}</th>
                <th class="text-right">{{ t('cost.official') }}</th>
                <th class="text-right">{{ t('cost.saved') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="k in costItems" :key="k.upstream_key_id">
                <td>{{ k.key_name || '#' + k.upstream_key_id }}</td>
                <td>
                  <span v-if="k.matched" class="text-gray-700 dark:text-dark-300">{{ k.account_name || '#' + k.account_id }}</span>
                  <span v-else class="text-xs text-amber-600 dark:text-amber-400" :title="t('cost.keyUnmatchedHint')">{{ t('cost.keyUnmatched') }}</span>
                </td>
                <td class="text-xs">{{ k.rate_multiplier !== null && k.rate_multiplier !== undefined ? '×' + k.rate_multiplier : '-' }}</td>
                <td class="text-right font-semibold">{{ fmtMoney(k.actual_cost) }}</td>
                <td class="text-right text-gray-400">{{ fmtMoney(k.official_cost) }}</td>
                <td class="text-right text-xs text-emerald-600 dark:text-emerald-400">{{ fmtMoney(k.official_cost - k.actual_cost) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </BaseDialog>

    <!-- 站点设置：余额阈值覆盖 -->
    <BaseDialog :show="showSiteSettings" :title="t('provider.siteSettings')" @close="showSiteSettings = false">
      <form id="site-settings-form" class="space-y-4" @submit.prevent="saveSiteSettings">
        <p class="text-sm text-gray-500 dark:text-dark-400">
          {{ currentProvider?.name }}
        </p>
        <div class="flex items-center justify-between gap-4 rounded-lg bg-gray-50 p-3 dark:bg-dark-800/50">
          <div>
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('provider.customThreshold') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('provider.customThresholdHint') }}</p>
          </div>
          <ToggleSwitch v-model="useCustomThreshold" />
        </div>
        <div v-if="useCustomThreshold">
          <label class="input-label">{{ t('provider.lowBalance') }}</label>
          <div class="relative max-w-xs">
            <span class="absolute left-3 top-1/2 -translate-y-1/2 text-sm text-gray-400">¥</span>
            <input v-model.number="siteThreshold" type="number" min="0" step="0.01" class="input !pl-7" />
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('provider.rechargeRate') }}</label>
          <input v-model.number="siteRechargeRate" type="number" min="0.01" step="0.01" class="input !w-32" />
          <p class="mt-1 text-xs text-gray-400">{{ t('provider.rechargeRateHint') }}</p>
        </div>
        <div class="flex items-center justify-between gap-4 rounded-lg bg-gray-50 p-3 dark:bg-dark-800/50">
          <div>
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('provider.ignoreBalanceAlert') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('provider.ignoreBalanceAlertHint') }}</p>
          </div>
          <ToggleSwitch v-model="siteIgnoreBalanceAlert" />
        </div>
        <div class="flex items-center justify-between gap-4 rounded-lg bg-gray-50 p-3 dark:bg-dark-800/50">
          <div>
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('provider.selfOperated') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('provider.selfOperatedHint') }}</p>
          </div>
          <ToggleSwitch v-model="siteSelfOperated" />
        </div>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showSiteSettings = false">{{ t('common.cancel') }}</button>
        <button type="submit" form="site-settings-form" class="btn btn-primary" :disabled="saving">
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </template>
    </BaseDialog>

    <!-- 可用分组 -->
    <BaseDialog :show="showGroups" :title="t('provider.viewGroups')" width="wide" @close="showGroups = false">
      <p class="mb-3 text-sm text-gray-500 dark:text-dark-400">{{ currentProvider?.name }}</p>
      <LoadingState v-if="groupsLoading" />
      <EmptyState v-else-if="!upstreamGroups.length" icon="sort" :title="t('provider.noGroups')" />
      <div v-else class="max-h-[60vh] space-y-5 overflow-y-auto">
        <div v-for="sec in groupedGroups" :key="sec.platform" class="space-y-2">
          <h4
            v-if="showPlatformSections"
            class="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-400"
          >
            <span class="h-1.5 w-1.5 rounded-full bg-primary-500"></span>
            {{ sec.label }}
            <span class="font-normal normal-case tracking-normal">({{ sec.groups.length }})</span>
          </h4>
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <div
              v-for="g in sec.groups" :key="g.id"
              class="rounded-xl border border-gray-200 p-3 text-center dark:border-dark-700"
              :class="{ 'opacity-50': g.deleted }"
            >
              <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="g.entity_name">
                {{ g.entity_name }}
              </p>
              <p class="mt-1 inline-block rounded-md bg-primary-50 px-2 py-0.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
                ×{{ fmtRate(g.rate) }}
              </p>
              <p v-if="currentProvider && currentProvider.recharge_rate > 0" class="mt-1 text-[10px] text-gray-400">
                ≈ ×{{ (g.rate * currentProvider.recharge_rate).toFixed(2) }} CNY
              </p>
            </div>
          </div>
        </div>
      </div>
    </BaseDialog>

    <!-- 删除确认 -->
    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('common.delete')"
      :message="pendingDelete ? t('common.confirmDelete', { name: pendingDelete.name }) : ''"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteConfirm = false"
    />

    <!-- 全部刷新确认 -->
    <ConfirmDialog
      :show="showRefreshAllConfirm"
      :title="t('provider.refreshAllConfirmTitle')"
      :message="t('provider.refreshAllConfirm', { n: collectableCount })"
      @confirm="confirmRefreshAll"
      @cancel="showRefreshAllConfirm = false"
    />

    <!-- 全部刷新的失败明细：仅在有失败站点时弹出 -->
    <BaseDialog
      :show="!!refreshAllFailures.length"
      :title="t('provider.refreshAllFailuresTitle')"
      @close="refreshAllFailures = []"
    >
      <ul class="space-y-2">
        <li
          v-for="f in refreshAllFailures"
          :key="f.provider_id"
          class="rounded-lg border border-red-200 bg-red-50/60 px-3 py-2 dark:border-red-900/40 dark:bg-red-900/10"
        >
          <p class="text-sm font-medium text-gray-900 dark:text-white">{{ f.name }}</p>
          <p class="mt-0.5 break-all text-xs text-red-600 dark:text-red-400">{{ f.error }}</p>
        </li>
      </ul>
      <template #footer>
        <button class="btn btn-secondary text-sm" @click="refreshAllFailures = []">
          {{ t('common.close') }}
        </button>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { providerApi, rateApi, settingsApi, type KeyCostsResult, type ProviderPayload, type RefreshAllFailure, type TestConnectionPayload } from '@/api'
import { errorMessage } from '@/api/client'
import { fmtDateTime, fmtMoney, fmtNum, todayStr } from '@/utils/format'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/Pagination.vue'
import LineChart from '@/components/LineChart.vue'
import Badge from '@/components/common/Badge.vue'
import TableState from '@/components/common/TableState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ToggleSwitch from '@/components/common/ToggleSwitch.vue'
import ProviderCard from '@/components/ProviderCard.vue'
import ProviderToolbar from '@/components/ProviderToolbar.vue'
import ProviderLinkDialog from '@/components/provider/ProviderLinkDialog.vue'
import OperatingCostDialog from '@/components/provider/OperatingCostDialog.vue'
import Select from '@/components/common/Select.vue'
import { platformLabel } from '@/utils/plazaModel'
import {
  searchProviders, filterProviders, sortProviders, providerStatus, isLowBalance,
  platformOptions, statusOptions, balanceTypeOptions,
  type ProviderSortKey, type ProviderStatus
} from '@/utils/providerModel'
import type { Provider, ProviderAccount, ScanItem, URLGroupItem, BalanceHistoryItem, RateSnapshotItem } from '@/types'

const { t } = useI18n()
const app = useAppStore()

const providers = ref<Provider[]>([])
const loading = ref(false)
const defaultBalanceThreshold = ref(0)

// 视图与筛选
const viewMode = ref<'card' | 'list'>('card')
const search = ref('')
const refreshingId = ref<number | null>(null)

// 搜索框输入与实际生效的查询分离，中间加防抖（项目无 @vueuse，手写）
const searchQuery = ref('')
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(search, (v) => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchQuery.value = v
  }, 300)
})

const platformFilter = ref<string | null>(null)
const statusFilter = ref<ProviderStatus | null>(null)
const balanceTypeFilter = ref<string | null>(null)
const sortKey = ref<ProviderSortKey>('todayCostDesc')

const searched = computed(() => searchProviders(providers.value, searchQuery.value))
// 筛选项计数基于搜索结果，随搜索联动
const platformOpts = computed(() => platformOptions(searched.value))
const statusOpts = computed(() => statusOptions(searched.value))
const balanceTypeOpts = computed(() => balanceTypeOptions(searched.value))

const filteredProviders = computed(() =>
  sortProviders(
    filterProviders(searched.value, platformFilter.value, statusFilter.value, balanceTypeFilter.value),
    sortKey.value
  )
)

// 已连接 = 有过成功采集且当前无失败/冷却
const connectedCount = computed(
  () => providers.value.filter((p) => providerStatus(p) === 'connected').length
)

// 可自动采集的站点数（与后端 ListCollectable 的口径一致：列表接口只返回上游站）
const collectableCount = computed(
  () => providers.value.filter((p) => p.balance_type === 'sub2api').length
)

// 自动刷新提示：读系统设置的刷新策略
const refreshIntervalMin = ref(0)
const refreshHint = computed(() =>
  refreshIntervalMin.value > 0
    ? t('provider.autoRefreshOn', { n: refreshIntervalMin.value })
    : t('provider.autoRefreshOff')
)

// 表单
const showForm = ref(false)
const editing = ref<Provider | null>(null)
const saving = ref(false)
const formError = ref('')
const testing = ref(false)
const testResult = ref('')
const testOk = ref(false)
const emptyForm = (): ProviderPayload & {
  login_password?: string
  access_token?: string
  refresh_token?: string
} => ({
  name: '', note: '', balance_type: 'sub2api', platform: 'sub2api', auth_mode: 'password',
  base_url: '', login_email: '', login_password: '', access_token: '', refresh_token: '',
  upstream_user_id: '', low_balance_threshold: 0, recharge_rate: 1,
  probe_enabled: false, probe_model: '', ignore_balance_alert: false, self_operated: false
})
const form = ref(emptyForm())

// 切换平台时纠正不合法的认证模式组合
function onPlatformChange() {
  if (form.value.platform === 'new-api' && form.value.auth_mode === 'token') {
    form.value = { ...form.value, auth_mode: 'user_key' }
  }
  if (form.value.platform === 'sub2api' && form.value.auth_mode === 'user_key') {
    form.value = { ...form.value, auth_mode: 'token' }
  }
}

const formBalanceTypeOptions = computed(() =>
  ['sub2api', 'manual', 'none'].map((value) => ({
    value,
    label: t(`provider.balanceTypes.${value}`)
  }))
)

const formPlatformOptions = [
  { value: 'sub2api', label: 'sub2api' },
  { value: 'new-api', label: 'new-api' }
]

/** 令牌模式与平台绑定：sub2api 用 token，new-api 用 user_key，两者都支持密码登录。 */
const formAuthModeOptions = computed(() => {
  const platformMode = form.value.platform === 'new-api' ? 'user_key' : 'token'
  return ['password', platformMode].map((value) => ({
    value,
    label: t(`provider.authModes.${value}`)
  }))
})

// 扫描
const showScan = ref(false)
const scanTab = ref<'prefix' | 'url'>('prefix')
const scanning = ref(false)
const scanItems = ref<ScanItem[]>([])
const selectedNames = ref<string[]>([])
const importing = ref(false)
/** prefix → 站点地址输入框（预填后端扫出的地址，用户可改） */
const scanUrls = ref<Record<string, string>>({})
/**
 * 导入时给新建站点设的采集方式，默认 API 自动采集。
 * 缺凭据的站点不会真进采集队列（后端凭据门禁），建完站补账密即可开始采集。
 */
const importBalanceType = ref('sub2api')

// 按站点地址归组（不依赖【】命名习惯的发现方式）
const urlScanning = ref(false)
const urlGroups = ref<URLGroupItem[]>([])
/** base_url → 勾选的账号 id */
const urlSelected = ref<Record<string, number[]>>({})
/** base_url → 站点名输入框（预填后端建议名，用户可改） */
const urlGroupNames = ref<Record<string, string>>({})

// 关联账号弹窗（供应商行内入口）：取数与勾选状态都在 ProviderLinkDialog 内部
const showLinks = ref(false)
const linkProvider = ref<Provider | null>(null)

// 子账号
const showAccounts = ref(false)
const accountsLoading = ref(false)
const accounts = ref<ProviderAccount[]>([])
const currentProvider = ref<Provider | null>(null)

// 手动余额
const showManual = ref(false)
const manualBalance = ref(0)

// 历史
const showHistory = ref(false)
const historyLoading = ref(false)
const historyItems = ref<BalanceHistoryItem[]>([])
const historyPage = ref(1)
const historyPages = ref(1)
const historyTotal = ref(0)

const historyLabels = computed(() => [...historyItems.value].reverse().map((h) => (h.created_at || '').slice(5, 16)))
const historyDatasets = computed(() => [
  { label: t('provider.lastBalance'), data: [...historyItems.value].reverse().map((h) => h.balance ?? 0), borderColor: '#14b8a6' }
])

// 上游 per-key 成本明细
const showCosts = ref(false)
const costsLoading = ref(false)
const syncing = ref(false)
const costError = ref('')
const costs = ref<KeyCostsResult | null>(null)
const costStart = ref(todayStr())
const costEnd = ref(todayStr())

const costItems = computed(() => costs.value?.items || [])

const allSelected = computed(() => {
  const items = scanItems.value
  return items.length > 0 && items.every((i) => selectedNames.value.includes(i.prefix))
})

function balanceTypeLabel(bt: string) {
  if (bt === 'sub2api') return t('provider.balanceTypes.sub2api')
  if (bt === 'manual') return t('provider.balanceTypes.manual')
  return t('provider.balanceTypes.none')
}
function balanceTypeVariant(bt: string) {
  if (bt === 'sub2api') return 'primary' as const
  if (bt === 'manual') return 'warning' as const
  return 'gray' as const
}
// 采集健康点：绿=正常 黄=1-2 次失败 红=3+ 或登录冷却 灰=待配置凭据/未采集。
//
// 状态判据不在本地重写：providerModel.providerStatus 是全站唯一定义源，
// 这里只做 status → 颜色的映射。失败次数的黄/红分档是健康点独有的粒度
// （筛选维度不需要这么细），故在 error 分支内部再分。
const HEALTH_DOT_GRAY = 'bg-gray-300 dark:bg-dark-600'
function healthDotClass(p: Provider): string {
  switch (providerStatus(p)) {
    case 'connected':
      return 'bg-emerald-500'
    case 'error':
      if (p.login_cooldown_until) return 'bg-red-500'
      return (p.sync_state?.consecutive_failures ?? 0) < 3 ? 'bg-amber-500' : 'bg-red-500'
    default: // credentialsPending / pending / unmonitored
      return HEALTH_DOT_GRAY
  }
}
function healthTitle(p: Provider): string {
  if (providerStatus(p) === 'credentialsPending') return t('provider.credentialsMissing')
  const st = p.sync_state
  if (!st || (!st.last_run_at && !st.last_success_at)) return t('provider.syncNever')
  if (st.consecutive_failures > 0) {
    return t('provider.syncFailing', { n: st.consecutive_failures }) + (st.last_error ? '\n' + st.last_error : '')
  }
  return t('provider.syncHealthy') + (st.last_success_at ? ' · ' + st.last_success_at : '')
}

async function load() {
  loading.value = true
  try {
    const res = await providerApi.list(1, 100)
    providers.value = res.items
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.value = emptyForm()
  formError.value = ''
  testResult.value = ''
  showForm.value = true
}
function openEdit(p: Provider) {
  editing.value = p
  form.value = {
    name: p.name, note: p.note, balance_type: p.balance_type,
    platform: p.platform || 'sub2api', auth_mode: p.auth_mode || 'password',
    base_url: p.base_url, login_email: p.login_email, login_password: '',
    access_token: '', refresh_token: '', upstream_user_id: p.upstream_user_id || '',
    low_balance_threshold: p.low_balance_threshold, recharge_rate: p.recharge_rate || 1,
    probe_enabled: p.probe_enabled, probe_model: p.probe_model || '',
    ignore_balance_alert: p.ignore_balance_alert, self_operated: p.self_operated
  }
  formError.value = ''
  testResult.value = ''
  showForm.value = true
}

async function saveForm() {
  saving.value = true
  formError.value = ''
  try {
    const payload: ProviderPayload = {
      name: form.value.name, note: form.value.note, balance_type: form.value.balance_type,
      platform: form.value.platform, auth_mode: form.value.auth_mode,
      base_url: form.value.base_url, login_email: form.value.login_email,
      upstream_user_id: form.value.upstream_user_id,
      low_balance_threshold: form.value.low_balance_threshold,
      recharge_rate: form.value.recharge_rate,
      probe_enabled: form.value.probe_enabled,
      probe_model: form.value.probe_model || null,
      ignore_balance_alert: form.value.ignore_balance_alert,
      self_operated: form.value.self_operated
    }
    // 凭据：有值才提交，编辑时留空表示不修改
    if (form.value.login_password) {
      payload.login_password = form.value.login_password
    }
    if (form.value.access_token) {
      payload.access_token = form.value.access_token
    }
    if (form.value.refresh_token) {
      payload.refresh_token = form.value.refresh_token
    }
    if (editing.value) {
      await providerApi.update(editing.value.id, payload)
    } else {
      await providerApi.create(payload)
    }
    showForm.value = false
    await load()
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

async function testConn() {
  testing.value = true
  testResult.value = ''
  try {
    const res = await providerApi.testConnection({
      platform: form.value.platform || 'sub2api',
      auth_mode: form.value.auth_mode || 'password',
      base_url: form.value.base_url || '',
      email: form.value.login_email || '',
      password: form.value.login_password || '',
      access_token: form.value.access_token || '',
      user_id: form.value.upstream_user_id || ''
    })
    if (res.ok === false) {
      testOk.value = false
      testResult.value = '✗ ' + (res.error || t('common.failed'))
      return
    }
    testOk.value = true
    testResult.value = '✓ ' + t('common.success') + (res.balance !== null && res.balance !== undefined ? ' · 余额 ' + fmtMoney(res.balance) : '')
  } catch (e) {
    testOk.value = false
    testResult.value = '✗ ' + errorMessage(e)
  } finally {
    testing.value = false
  }
}

// 删除确认（两段式：先记录目标弹确认框，确认后执行）
const showDeleteConfirm = ref(false)
const pendingDelete = ref<Provider | null>(null)

function onDelete(p: Provider) {
  pendingDelete.value = p
  showDeleteConfirm.value = true
}
async function confirmDelete() {
  const p = pendingDelete.value
  showDeleteConfirm.value = false
  if (!p) return
  try {
    await providerApi.remove(p.id)
    app.showSuccess(t('common.success'))
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    pendingDelete.value = null
  }
}

async function openScan() {
  showScan.value = true
  scanTab.value = 'prefix'
  await loadPrefixScan()
}

function switchScanTab(tab: 'prefix' | 'url') {
  scanTab.value = tab
  if (tab === 'url' && !urlGroups.value.length) loadUrlScan()
}

async function loadPrefixScan() {
  scanning.value = true
  scanItems.value = []
  selectedNames.value = []
  try {
    const res = await providerApi.scan()
    scanItems.value = res.items || []
    // 默认只勾未建站的：已建站的行保留可勾选（补关联用），但不替用户做决定
    selectedNames.value = scanItems.value.filter((i) => !i.exists).map((i) => i.prefix)
    const urls: Record<string, string> = {}
    for (const i of scanItems.value) urls[i.prefix] = i.base_url
    scanUrls.value = urls
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    scanning.value = false
  }
}

async function loadUrlScan() {
  urlScanning.value = true
  try {
    const res = await providerApi.scanUrls()
    urlGroups.value = res.items || []
    // 每组独立的勾选态与站点名：默认全选未归属的账号，站点名预填后端建议名
    const sel: Record<string, number[]> = {}
    const names: Record<string, string> = {}
    for (const g of urlGroups.value) {
      sel[g.base_url] = g.accounts.filter((a) => !a.linked_to).map((a) => a.id)
      names[g.base_url] = g.existing_provider || g.suggested_name
    }
    urlSelected.value = sel
    urlGroupNames.value = names
  } catch (e) {
    app.showError(errorMessage(e))
    urlGroups.value = []
  } finally {
    urlScanning.value = false
  }
}

function isUrlGroupAllSelected(g: URLGroupItem): boolean {
  const sel = urlSelected.value[g.base_url] || []
  return g.accounts.length > 0 && sel.length === g.accounts.length
}

function toggleUrlGroup(g: URLGroupItem) {
  urlSelected.value[g.base_url] = isUrlGroupAllSelected(g) ? [] : g.accounts.map((a) => a.id)
}

/** 建站（已存在则复用）并把勾选的账号关联过去 */
async function importUrlGroup(g: URLGroupItem) {
  const name = (urlGroupNames.value[g.base_url] || '').trim()
  const ids = urlSelected.value[g.base_url] || []
  if (!name || !ids.length) return
  importing.value = true
  try {
    const res = await providerApi.import([
      { name, base_url: g.base_url, balance_type: importBalanceType.value, account_ids: ids }
    ])
    app.showSuccess(t('provider.linkedResult', { name, n: res.linked }))
    await Promise.all([load(), loadUrlScan()])
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    importing.value = false
  }
}

function toggleAll() {
  selectedNames.value = allSelected.value ? [] : scanItems.value.map((i) => i.prefix)
}

async function doImport() {
  importing.value = true
  try {
    // 顺带把该前缀下的账号一并关联 —— 前缀只用来发现，归属靠关联表落库
    const items = selectedNames.value.map((name) => {
      const hit = scanItems.value.find((i) => i.prefix === name)
      return {
        name,
        base_url: scanUrls.value[name] || '',
        balance_type: importBalanceType.value,
        account_ids: hit?.account_ids || []
      }
    })
    const res = await providerApi.import(items)
    app.showSuccess(
      t('provider.importedLinked', {
        created: res.created.length,
        skipped: res.skipped.length,
        linked: res.linked
      })
    )
    showScan.value = false
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    importing.value = false
  }
}

/** 打开关联弹窗：账号列表与勾选态由弹窗自己在 show 变 true 时加载 */
function openLinks(p: Provider) {
  linkProvider.value = p
  showLinks.value = true
}

async function openAccounts(p: Provider) {
  currentProvider.value = p
  showAccounts.value = true
  accountsLoading.value = true
  accounts.value = []
  try {
    const res = await providerApi.accounts(p.id)
    accounts.value = res.items || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    accountsLoading.value = false
  }
}

async function refreshBalance(p: Provider) {
  refreshingId.value = p.id
  try {
    await providerApi.refreshBalance(p.id)
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    refreshingId.value = null
  }
}

// ---- 全部刷新 ----
// 后端一次请求内并发采集全部站点：前端只管发起与展示汇总，不在浏览器侧并发，
// 免得 N 个请求撞上 axios 的 120s 超时和浏览器并发上限。
const refreshingAll = ref(false)
const showRefreshAllConfirm = ref(false)
const refreshAllFailures = ref<RefreshAllFailure[]>([])

function openRefreshAll() {
  showRefreshAllConfirm.value = true
}

async function confirmRefreshAll() {
  showRefreshAllConfirm.value = false
  refreshingAll.value = true
  try {
    const r = await providerApi.refreshAll()
    if (!r.total && !r.skipped) {
      app.showInfo(t('provider.refreshAllNone'))
      return
    }
    let msg = t('provider.refreshAllDone', { ok: r.succeeded })
    if (r.failed) msg += t('provider.refreshAllFailedPart', { n: r.failed })
    if (r.skipped) msg += t('provider.refreshAllSkippedPart', { n: r.skipped })
    if (r.failed) {
      app.showWarning(msg)
      refreshAllFailures.value = r.failures || []
    } else {
      app.showSuccess(msg)
    }
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    refreshingAll.value = false
  }
}

// ---- 站点设置（余额阈值覆盖 + 充值倍率）----
const showSiteSettings = ref(false)
const useCustomThreshold = ref(false)
const siteThreshold = ref(0)
const siteRechargeRate = ref(1)
const siteIgnoreBalanceAlert = ref(false)
const siteSelfOperated = ref(false)

// 运营成本弹窗（仅自营站）：取数与表单状态都在 OperatingCostDialog 内部
const showOpCosts = ref(false)
const opCostProvider = ref<Provider | null>(null)

function openOpCosts(p: Provider) {
  opCostProvider.value = p
  showOpCosts.value = true
}

function openSiteSettings(p: Provider) {
  currentProvider.value = p
  useCustomThreshold.value = p.low_balance_threshold > 0
  siteThreshold.value = p.low_balance_threshold || 0
  siteRechargeRate.value = p.recharge_rate || 1
  siteIgnoreBalanceAlert.value = p.ignore_balance_alert
  siteSelfOperated.value = p.self_operated
  showSiteSettings.value = true
}

// 复用 provider 更新接口：阈值置 0 表示回落全局默认
async function saveSiteSettings() {
  const p = currentProvider.value
  if (!p) return
  saving.value = true
  try {
    await providerApi.update(p.id, {
      name: p.name,
      note: p.note,
      balance_type: p.balance_type,
      platform: p.platform,
      auth_mode: p.auth_mode,
      base_url: p.base_url,
      login_email: p.login_email,
      upstream_user_id: p.upstream_user_id,
      low_balance_threshold: useCustomThreshold.value ? siteThreshold.value : 0,
      recharge_rate: siteRechargeRate.value,
      probe_enabled: p.probe_enabled,
      probe_model: p.probe_model || null,
      ignore_balance_alert: siteIgnoreBalanceAlert.value,
      // 必须显式带上：update 是全字段覆盖，省略等同 false 会静默清除自营标记
      self_operated: siteSelfOperated.value
    })
    showSiteSettings.value = false
    app.showSuccess(t('common.success'))
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    saving.value = false
  }
}

// ---- 可用分组（读上游倍率快照）----
const showGroups = ref(false)
const groupsLoading = ref(false)
const upstreamGroups = ref<RateSnapshotItem[]>([])

async function openGroups(p: Provider) {
  currentProvider.value = p
  showGroups.value = true
  groupsLoading.value = true
  upstreamGroups.value = []
  try {
    const res = await rateApi.current({ scope: 'upstream', provider_id: p.id })
    upstreamGroups.value = res.items || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    groupsLoading.value = false
  }
}

function fmtRate(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(4).replace(/\.?0+$/, '')
}

// 主流平台优先展示，其余按字典序，未分类恒定垫底
const PLATFORM_ORDER = ['anthropic', 'openai', 'gemini', 'antigravity']

// groupedGroups 按 platform 动态分桶。
// 不用常量映射表：上游新增平台时自动出现新分节，无需改前端。
const groupedGroups = computed(() => {
  const buckets = upstreamGroups.value.reduce<Record<string, RateSnapshotItem[]>>((acc, g) => {
    const key = g.platform || ''
    ;(acc[key] ||= []).push(g)
    return acc
  }, {})
  return Object.keys(buckets)
    .sort((a, b) => {
      // 空串（未分类）永远最后，与已知平台的次序无关
      if (!a !== !b) return a ? -1 : 1
      const ia = PLATFORM_ORDER.indexOf(a)
      const ib = PLATFORM_ORDER.indexOf(b)
      if (ia !== ib) return (ia < 0 ? PLATFORM_ORDER.length : ia) - (ib < 0 ? PLATFORM_ORDER.length : ib)
      return a.localeCompare(b)
    })
    .map(platform => ({
      platform,
      label: platform ? platformLabel(platform) : t('provider.uncategorizedPlatform'),
      groups: buckets[platform]
    }))
})

// showPlatformSections 只要上游给出过任一平台归属就分节展示。
//
// 不能用「桶数 > 1」当判据：整站分组同属一个平台（如纯 Claude 中转）时桶数为 1，
// 标题会被隐藏，看起来就是没分类。反之全站都没平台归属时，一个「未分类」标题纯属噪音。
const showPlatformSections = computed(() => groupedGroups.value.some(s => s.platform !== ''))

function openManual(p: Provider) {
  currentProvider.value = p
  manualBalance.value = p.last_balance ?? 0
  showManual.value = true
}
async function saveManual() {
  if (!currentProvider.value) return
  saving.value = true
  try {
    await providerApi.manualBalance(currentProvider.value.id, manualBalance.value)
    showManual.value = false
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    saving.value = false
  }
}

async function openHistory(p: Provider) {
  currentProvider.value = p
  showHistory.value = true
  await loadHistory(1)
}
async function loadHistory(page: number) {
  if (!currentProvider.value) return
  historyLoading.value = true
  try {
    const res = await providerApi.balanceHistory(currentProvider.value.id, page, 50)
    historyItems.value = res.items
    historyPage.value = res.page
    historyPages.value = res.pages
    historyTotal.value = res.total
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    historyLoading.value = false
  }
}

async function openCosts(p: Provider) {
  currentProvider.value = p
  showCosts.value = true
  costStart.value = todayStr()
  costEnd.value = todayStr()
  await loadCosts()
}

async function loadCosts() {
  if (!currentProvider.value) return
  costsLoading.value = true
  costError.value = ''
  try {
    costs.value = await providerApi.keyCosts(currentProvider.value.id, costStart.value, costEnd.value)
  } catch (e) {
    costError.value = errorMessage(e)
    costs.value = null
  } finally {
    costsLoading.value = false
  }
}

// backfill=true 会逐 key 拉取 90 天历史，慢但只需做一次
async function doSync(backfill: boolean) {
  if (!currentProvider.value) return
  syncing.value = true
  costError.value = ''
  try {
    await providerApi.syncCost(currentProvider.value.id, backfill)
    await loadCosts()
  } catch (e) {
    costError.value = errorMessage(e)
  } finally {
    syncing.value = false
  }
}

onMounted(async () => {
  await load()
  // 策略同时驱动自动刷新提示与全局余额阈值；读取失败不影响主列表。
  try {
    const st = await settingsApi.getStrategy()
    refreshIntervalMin.value = st.strategy.refresh_enabled
      ? Math.round(st.strategy.refresh_interval_seconds / 60)
      : 0
    defaultBalanceThreshold.value = st.strategy.default_balance_threshold
  } catch {
    refreshIntervalMin.value = 0
    defaultBalanceThreshold.value = 0
  }
})
</script>
