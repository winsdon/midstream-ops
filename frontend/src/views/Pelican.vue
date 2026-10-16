<template>
  <div class="space-y-4">
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
    <PelicanGallery ref="gallery" admin :groups="groups" :accounts="accounts">
      <template #actions>
        <button type="button" class="btn btn-primary btn-sm" @click="openComposer">
          <Icon name="play" size="sm" />
          {{ t('pelican.startTest') }}
        </button>
      </template>
    </PelicanGallery>
    <PelicanComposer
      v-model:draft="draft"
      :show="composerOpen"
      :groups="groups"
      :accounts="accounts"
      :submitting="submitting"
      :pending="pending !== null"
      :error="composerError"
      :run-count="runCount"
      :replayed="replayed"
      :default-prompt="defaultPrompt"
      @submit="submit"
      @discard="clearPending"
      @close="closeComposer"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { pelicanAPI } from '@/api/pelican'
import { errorMessage } from '@/api/client'
import Icon from '@/components/icons/Icon.vue'
import PelicanComposer from '@/components/pelican/PelicanComposer.vue'
import PelicanGallery from '@/components/pelican/PelicanGallery.vue'
import type { PelicanAccount, PelicanGroup, PelicanRequest } from '@/types/pelican'
import {
  pelicanAccountsForGroup,
  pelicanDefaultPrompt,
  pelicanDraftFingerprint,
  pelicanDraftFromRequest,
  pelicanDraftTargets,
  pelicanEmptyDraft,
  pelicanIsDefaultPrompt,
  pelicanPruneAccountIDs,
  pelicanRequestID,
  type PelicanDraft
} from '@/utils/pelican'

const { t } = useI18n()
const groups = ref<PelicanGroup[]>([])
const accounts = ref<PelicanAccount[]>([])
const error = ref('')
/** 提交失败只写在弹窗里，页面级错误留给拉取配置失败。 */
const composerError = ref('')
const notice = ref('')
const draft = ref<PelicanDraft>(pelicanEmptyDraft())
const composerOpen = ref(false)
const submitting = ref(false)
const pending = ref<PelicanRequest | null>(null)
/** 从待确认提交恢复出的表单指纹：一致就原样重放，沿用同一个请求标识。 */
let pendingFingerprint: string | null = null
const gallery = ref<InstanceType<typeof PelicanGallery>>()

/** 所选分组下可选的本站账号：后端按 account_groups 给出归属，前端只做匹配。 */
const available = computed(() => pelicanAccountsForGroup(accounts.value, draft.value.group_id))
const targets = computed(() => pelicanDraftTargets(draft.value, available.value))
/** 当前目标对应的默认题目：全部 Claude（Messages）用初音未来，否则用考拉。 */
const defaultPrompt = computed(() => pelicanDefaultPrompt(targets.value))
/**
 * 提示词还是默认题目时跟着目标协议切换；管理员改过的不动。
 * 有待确认提交时不切：提示词变了指纹就对不上，原样重放会变成新测试。
 */
watch(defaultPrompt, next => {
  if (pending.value || !pelicanIsDefaultPrompt(draft.value.prompt) || draft.value.prompt === next) return
  draft.value = { ...draft.value, prompt: next }
})
/** 待确认提交里不带 Key，写 sessionStorage 前必须再剥一次。 */
function sanitize(request: PelicanRequest): PelicanRequest {
  return { ...request, targets: request.targets.map(({ api_key: _apiKey, ...target }) => target) }
}
function persistPending() {
  if (pending.value) sessionStorage.setItem('pelican.pending', JSON.stringify(sanitize(pending.value)))
  else sessionStorage.removeItem('pelican.pending')
}
function clearPending() {
  pending.value = null
  pendingFingerprint = null
  composerError.value = ''
  persistPending()
}
/** 参数没变时沿用同一个 request_id：重试不会重复执行，改了参数则算新测试。 */
function samePending(next: PelicanRequest) {
  if (!pending.value) return false
  const strip = (request: PelicanRequest) => JSON.stringify({ ...request, request_id: '' })
  return strip(pending.value) === strip(next)
}
/**
 * 原样重放待确认提交：目标用落库前存下的那一份，只把手填 Key 补回去。
 *
 * 不能拿当前表单重算目标：恢复后按分组收敛掉的账号、账号改名、精选分组变化都会让
 * 重算结果与上次不同，请求标识就换了 —— 上一次其实已经受理的批次会被再跑一遍，
 * 管理员白花钱。
 */
function replayPending(stored: PelicanRequest): PelicanRequest {
  return { ...stored, targets: stored.targets.map(target => (target.source === 'manual' ? { ...target, api_key: draft.value.api_key.trim() } : target)) }
}
/** 待确认提交恢复后参数没动过，就按原请求重放。 */
function replayable() {
  return pending.value !== null && pendingFingerprint !== null && pendingFingerprint === pelicanDraftFingerprint(draft.value)
}
/** 本次提交会跑多少个目标：重放时按存下来的那份，否则按当前表单。 */
const runCount = computed(() => (replayable() ? (pending.value?.targets.length ?? 0) : targets.value.length))
const replayed = computed(() => replayable())
function openComposer() {
  composerOpen.value = true
}
/** 提交中不允许关弹窗：关掉以后失败信息与待确认提交就没了落点。 */
function closeComposer() {
  if (!submitting.value) composerOpen.value = false
}
async function submit() {
  if (submitting.value || !runCount.value) return
  submitting.value = true
  composerError.value = ''
  notice.value = ''
  const draftRequest: PelicanRequest = {
    request_id: '',
    prompt: draft.value.prompt,
    concurrency: draft.value.concurrency,
    max_tokens: draft.value.max_tokens,
    timeout_seconds: draft.value.timeout_seconds,
    ...(draft.value.effort ? { effort: draft.value.effort } : {}),
    targets: targets.value
  }
  const payload = replayable()
    ? replayPending(pending.value!)
    : { ...draftRequest, request_id: samePending(draftRequest) ? pending.value!.request_id : pelicanRequestID() }
  pending.value = payload
  pendingFingerprint = pelicanDraftFingerprint(draft.value)
  persistPending()
  try {
    await pelicanAPI.start(payload)
    clearPending()
    composerOpen.value = false
    notice.value = t('pelican.submitted')
    await gallery.value?.refresh()
  } catch (e) { composerError.value = t(errorMessage(e)) } finally { submitting.value = false }
}
/** 表单按待确认提交回填（手填 Key 除外），重新提交时 hash 才能与上次一致。 */
function restorePending() {
  let raw = ''
  try { raw = sessionStorage.getItem('pelican.pending') ?? '' } catch { raw = '' }
  if (!raw) return
  let stored: PelicanRequest
  try { stored = JSON.parse(raw) } catch { clearPending(); return }
  const restored = pelicanDraftFromRequest(stored)
  // 旧版多行目标的形状这套「一个分组 + 多个账号」的表单表达不了，按新测试处理。
  if (!restored) { clearPending(); return }
  pending.value = stored
  draft.value = restored
  pendingFingerprint = pelicanDraftFingerprint(restored)
  // 待确认提交只可能来自本页的发起测试：恢复后直接把弹窗打开，避免状态藏在页面里。
  composerOpen.value = true
}
/**
 * 账号列表到位后补齐默认值：没有账号就只能手填，分组和账号已失效时同步收敛。
 * 有待确认提交时只收敛账号勾选并记下新的指纹 —— 其余「补默认值」的动作会改变参数、
 * 破坏幂等重放。
 */
function hydrate() {
  const next = { ...draft.value }
  if (!pending.value && !accounts.value.length) next.source = 'manual'
  if (!pending.value && !(next.group_id > 0) && groups.value.length === 1) next.group_id = groups.value[0].id
  const list = pelicanAccountsForGroup(accounts.value, next.group_id)
  next.account_ids = pelicanPruneAccountIDs(next.account_ids, list)
  if (!pending.value && next.source === 'account' && !next.account_ids.length && list.length === 1) next.account_ids = [list[0].id]
  draft.value = next
  if (pending.value) pendingFingerprint = pelicanDraftFingerprint(next)
}
onMounted(async () => {
  restorePending()
  try {
    const [groupData, accountData] = await Promise.all([pelicanAPI.groups(), pelicanAPI.accounts()])
    groups.value = groupData.items
    accounts.value = accountData.items
    hydrate()
  } catch (e) { error.value = t(errorMessage(e)) }
})
</script>
