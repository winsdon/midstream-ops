import { onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { detectApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import type { DetectJob, DetectRetryPayload, DetectRunPayload } from '@/types/detect'

/** 作业轮询间隔：要跟上「执行中」格子，间隔不宜超过 1s。 */
const POLL_INTERVAL_MS = 800

/**
 * 一份检测作业的生命周期：发起 → 轮询快照 → 取消 / 重试。
 *
 * 模型真伪与智商测试各持一份，切换模式不会互相覆盖；组件卸载时自动停表。
 * onFinished 在作业结束（完成或取消）后回调，调用方据此刷新对应的历史。
 */
export function useDetectJob(options: { onFinished?: (job: DetectJob) => void } = {}) {
  const { t } = useI18n()
  const app = useAppStore()

  const job = ref<DetectJob | null>(null)
  const running = ref(false)
  let pollTimer: ReturnType<typeof setInterval> | null = null
  /**
   * 轮询代次。停表或换一轮轮询都会让代次加一，之前发出、之后才回来的响应一律丢弃——
   * 智商测试的快照带作品与回复全文，单次请求可能比轮询间隔还慢，晚到的旧快照不能盖掉新结果。
   */
  let generation = 0

  function stopPolling(): void {
    generation++
    if (pollTimer) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  function startPolling(jobId: string): void {
    stopPolling()
    const gen = generation
    // 同一轮里只留一个在途请求，避免响应乱序
    let inflight = false
    const tick = async (): Promise<void> => {
      if (inflight) return
      inflight = true
      try {
        const snap = await detectApi.job(jobId)
        if (gen !== generation) return
        job.value = snap
        if (snap.status !== 'running') {
          stopPolling()
          running.value = false
          app.showSuccess(snap.status === 'cancelled' ? t('detect.cancelled') : t('detect.completed'))
          options.onFinished?.(snap)
        }
      } catch (e) {
        // 单次轮询失败不终止：可能只是网络抖动，下一拍会补上
        if (gen === generation && errorMessage(e).includes('404')) {
          stopPolling()
          running.value = false
        }
      } finally {
        inflight = false
      }
    }
    pollTimer = setInterval(() => void tick(), POLL_INTERVAL_MS)
    void tick()
  }

  async function start(payload: DetectRunPayload): Promise<void> {
    running.value = true
    job.value = null
    try {
      const res = await detectApi.run(payload)
      startPolling(res.job_id)
    } catch (e) {
      running.value = false
      app.showError(t('detect.runFailed') + '：' + errorMessage(e))
    }
  }

  async function cancel(): Promise<void> {
    if (!job.value) return
    try {
      await detectApi.cancel(job.value.id)
    } catch (e) {
      app.showError(errorMessage(e))
    }
  }

  async function retry(payload: DetectRetryPayload): Promise<void> {
    if (!job.value) return
    running.value = true
    try {
      await detectApi.retry(job.value.id, payload)
      startPolling(job.value.id)
    } catch (e) {
      running.value = false
      app.showError(t('detect.retryFailedStart') + '：' + errorMessage(e))
    }
  }

  onUnmounted(stopPolling)

  return { job, running, start, cancel, retry }
}
