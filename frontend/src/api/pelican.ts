import { http, unwrap } from './client'
import { createEmbedClient } from './embedClient'
import type { PelicanAccount, PelicanBatch, PelicanFacet, PelicanGroup, PelicanPage, PelicanRequest, PelicanResult } from '@/types/pelican'

export const pelicanAPI = {
  groups: () => unwrap<{ items: PelicanGroup[] }>(http.get('/pelican/groups')),
  accounts: () => unwrap<{ items: PelicanAccount[] }>(http.get('/pelican/accounts')),
  start: (request: PelicanRequest) => unwrap<PelicanBatch>(http.post('/pelican/batches', request)),
  batch: (id: number) => unwrap<{ batch: PelicanBatch; items: PelicanResult[] }>(http.get(`/pelican/batches/${id}`)),
  cancel: (id: number) => unwrap(http.post(`/pelican/batches/${id}/cancel`)),
  list: (query: string) => unwrap<PelicanPage>(http.get(`/pelican/results?${query}`)),
  detail: (id: number) => unwrap<PelicanResult>(http.get(`/pelican/results/${id}`)),
  mutate: (action: string, ids: number[]) => unwrap(http.post(`/pelican/results/actions/${action}`, { ids })),
  /** 手填 Key 没有落库，manual 来源重新测试时必须再传一次 api_key。 */
  retry: (id: number, body: { request_id: string; account_id?: number; api_key?: string }) => unwrap<PelicanBatch>(http.post(`/pelican/results/${id}/retry`, body))
}
const embed = createEmbedClient('/api/v1/embed/pelican')
export const embedPelicanAPI = {
  createSession: embed.createSession,
  list: (query: string) => embed.request<PelicanPage>(`/results?${query}`),
  detail: (id: number) => embed.request<PelicanResult>(`/results/${id}`),
  filters: () => embed.request<{ items: PelicanFacet[] }>('/filters')
}
