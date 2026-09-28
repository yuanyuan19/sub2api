import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useAppStore } from '@/stores/app'
import VersionBadge from '../VersionBadge.vue'

const { checkUpdates } = vi.hoisted(() => ({ checkUpdates: vi.fn() }))
vi.mock('@/api/admin/system', () => ({
  checkUpdates,
  performUpdate: vi.fn(),
  restartService: vi.fn(),
  getRollbackVersions: vi.fn(),
  rollback: vi.fn()
}))
vi.mock('@/stores', async () => {
  const { useAppStore } = await import('@/stores/app')
  return { useAppStore, useAuthStore: () => ({ isAdmin: true }) }
})
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
enableAutoUnmount(afterEach)
beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

async function openBadge(disabled: boolean, hasUpdate: boolean, buildType = 'release') {
  checkUpdates.mockResolvedValue({
    current_version: '0.2.8+mainstation.2',
    latest_version: hasUpdate ? '0.2.9' : '0.2.8',
    has_update: hasUpdate,
    updates_disabled: disabled,
    build_type: buildType,
    cached: false,
    release_info: { html_url: 'https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.8' }
  })
  const store = useAppStore()
  await store.fetchVersion(true)
  expect((await store.fetchVersion(false))?.updates_disabled).toBe(disabled)
  expect(checkUpdates).toHaveBeenCalledTimes(1)
  const wrapper = mount(VersionBadge, { global: { stubs: { Icon: true } } })
  await wrapper.get('button').trigger('click')
  return wrapper
}

describe('image-managed version badge', () => {
  it.each([false, true])('matches the source-build up-to-date panel with hasUpdate=%s', async hasUpdate => {
    const wrapper = await openBadge(true, hasUpdate)
    expect(wrapper.get('button').text()).toBe('v0.2.8+mainstation.2')
    expect(wrapper.text()).toContain('v0.2.8+mainstation.2')
    expect(wrapper.text()).toContain('version.upToDate')
    expect(wrapper.text()).toContain('version.rollback')
    expect(wrapper.text()).not.toContain('version.updateNow')
    expect(wrapper.text()).not.toContain('version.updateAvailable')
    expect(wrapper.text()).not.toContain('version.updatesDisabledHint')
    expect(wrapper.find('a').attributes('href')).toContain('/releases/tag/')

    const rollbackButton = wrapper
      .findAll('button')
      .find(button => button.text().includes('version.rollback'))
    expect(rollbackButton).toBeTruthy()
    await rollbackButton!.trigger('click')
    expect(wrapper.text()).toContain('version.updatesDisabledRollbackHint')
    expect(wrapper.text()).not.toContain('version.rollbackSelectVersion')
  })

  it('keeps update actions for normal release builds', async () => {
    const wrapper = await openBadge(false, true)
    expect(wrapper.text()).toContain('version.updateNow')
    expect(wrapper.text()).not.toContain('version.updatesDisabledHint')
  })

  it('keeps rollback actions for normal release builds', async () => {
    const wrapper = await openBadge(false, false)
    expect(wrapper.text()).toContain('version.rollback')
  })

  it('keeps the source-build hint', async () => {
    const wrapper = await openBadge(false, true, 'source')
    expect(wrapper.text()).toContain('version.sourceModeHint')
    expect(wrapper.text()).not.toContain('version.updateNow')
  })
})
