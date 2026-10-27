import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive, type EffectScope } from 'vue'
import ProviderProvisionDialog from './ProviderProvisionDialog.vue'
import ProviderGroupsDialog from './ProviderGroupsDialog.vue'

vi.mock('vue', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue')>(),
  useSSRContext: () => ({ modules: new Set<string>() })
}))

const api = vi.hoisted(() => ({
  connect: vi.fn(),
  get: vi.fn(async () => ({ configured: true })),
  localGroups: vi.fn(async () => ({ items: [
    { id: 1, name: 'vip', platform: 'openai', rate: 1 },
    { id: 2, name: 'vip', platform: 'anthropic', rate: 2 },
    { id: 3, name: 'gemini', platform: 'gemini', rate: 1 }
  ] }))
}))
vi.mock('@/api', () => ({ selfApi: api, provisionApi: api, providerApi: {}, rateApi: {} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

// Exercise the SFC setup state and handlers without a browser/DOM dependency.
const scopes: EffectScope[] = []
function setup(component: unknown, overrides = {}) {
  const props = reactive({
    show: false,
    provider: { id: 7, name: 'up', platform: 'new-api', base_url: 'https://up.invalid' },
    accounts: [],
    groups: [{ entity_name: 'vip', rate: 1, platform: 'openai' }],
    ...overrides
  })
  const scope = effectScope()
  scopes.push(scope)
  const emit = vi.fn()
  const state = scope.run(() => (component as { setup: Function }).setup(props, { expose: vi.fn(), emit }))
  return { state, props, emit }
}
afterEach(() => {
  for (const scope of scopes.splice(0)) scope.stop()
  api.connect.mockReset()
})

describe('new-api group provisioning', () => {
  it('allows selecting new-api groups but not deleted groups', () => {
    const { state } = setup(ProviderGroupsDialog)
    expect(state.canSelect({ deleted: false })).toBe(true)
    expect(state.canSelect({ deleted: true })).toBe(false)
    state.toggle({ entity_name: 'vip', deleted: false })
    expect(state.selected.value.has('vip')).toBe(true)
  })

  it('starts without a protocol and blocks submission', async () => {
    const { state } = setup(ProviderProvisionDialog)
    await state.hydrate()
    expect(state.rows.value[0].platform).toBe('')
    expect(state.rows.value[0].localGroupIds).toEqual([])
    expect(state.canSubmit.value).toBe(false)
    await state.submit()
    expect(api.connect).not.toHaveBeenCalled()
  })

  it('filters local groups and replaces incompatible selections on protocol changes', async () => {
    const { state } = setup(ProviderProvisionDialog)
    await state.hydrate()
    const row = state.rows.value[0]
    row.platform = 'openai'
    state.changePlatform(row)
    expect(row.localGroupIds).toEqual([1])
    row.platform = 'anthropic'
    state.changePlatform(row)
    expect(row.localGroupIds).toEqual([2])
    expect(state.compatibleGroups(row).map((g: { id: number }) => g.id)).toEqual([2])
    row.platform = 'gemini'
    state.changePlatform(row)
    expect(row.localGroupIds).toEqual([3])
    expect(state.canSubmit.value).toBe(true)
  })

  it('sends protocol, skips successful rows and reuses the uncertain operation id', async () => {
    const { state } = setup(ProviderProvisionDialog, { groups: [
      { entity_name: 'vip', rate: 1 }, { entity_name: 'second', rate: 2 }
    ] })
    await state.hydrate()
    for (const row of state.rows.value) { row.platform = 'openai'; state.changePlatform(row) }
    api.connect.mockResolvedValueOnce({ ok: true }).mockRejectedValueOnce(new Error('timeout'))
    await state.submit()
    expect(state.rows.value.map((r: { status: string }) => r.status)).toEqual(['ok', 'fail'])
    const secondRequest = api.connect.mock.calls[1][0]
    expect(secondRequest.platform).toBe('openai')
    expect(secondRequest.local_group_ids).toEqual([1])
    expect(secondRequest.operation_id).toBeTruthy()
    await state.hydrate() // Reopening the same unfinished batch keeps its progress.
    expect(state.rows.value.map((r: { status: string }) => r.status)).toEqual(['ok', 'fail'])
    api.connect.mockResolvedValueOnce({ ok: true })
    await state.submit()
    expect(api.connect).toHaveBeenCalledTimes(3)
    expect(api.connect.mock.calls[2][0]).toEqual(secondRequest)
    expect(state.canSubmit.value).toBe(false)
  })

  it('keeps sub2api protocol supplied by upstream and omits the new request field', async () => {
    const { state } = setup(ProviderProvisionDialog, { provider: { id: 8, platform: 'sub2api', name: 'sub', base_url: 'https://sub.invalid' } })
    await state.hydrate()
    expect(state.rows.value[0].platform).toBe('openai')
    api.connect.mockResolvedValueOnce({ ok: true })
    await state.submit()
    expect(api.connect.mock.calls[0][0].platform).toBeUndefined()
  })
})
