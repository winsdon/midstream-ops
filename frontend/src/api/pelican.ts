import { http, unwrap } from './client'
import { createEmbedClient } from './embedClient'
import type { PelicanBatch, PelicanConfig, PelicanFacet, PelicanGroup, PelicanPage, PelicanRequest, PelicanResult } from '@/types/pelican'

export const pelicanAPI = {
  config: () => unwrap<PelicanConfig>(http.get('/pelican/config')),
  saveConfig: (value: PelicanConfig) => unwrap<PelicanConfig>(http.put('/pelican/config', value)),
  groups: () => unwrap<{ items: PelicanGroup[] }>(http.get('/pelican/groups')),
  users: () => unwrap<{ items: { id: string; email: string; status: string }[] }>(http.get('/credit/sub2api-users')),
  start: (request: PelicanRequest) => unwrap<PelicanBatch>(http.post('/pelican/batches', request)),
  batch: (id: number) => unwrap<{ batch: PelicanBatch; items: PelicanResult[] }>(http.get(`/pelican/batches/${id}`)),
  cancel: (id: number) => unwrap(http.post(`/pelican/batches/${id}/cancel`)),
  list: (query: string) => unwrap<PelicanPage>(http.get(`/pelican/results?${query}`)),
  detail: (id: number) => unwrap<PelicanResult>(http.get(`/pelican/results/${id}`)),
  mutate: (action: string, ids: number[]) => unwrap(http.post(`/pelican/results/actions/${action}`, { ids })),
  retry: (id: number, request_id: string, key_id?: number) => unwrap<PelicanBatch>(http.post(`/pelican/results/${id}/retry`, { request_id, key_id }))
}
const embed = createEmbedClient('/api/v1/embed/pelican')
export const embedPelicanAPI = {
  createSession: embed.createSession,
  list: (query: string) => embed.request<PelicanPage>(`/results?${query}`),
  detail: (id: number) => embed.request<PelicanResult>(`/results/${id}`),
  filters: () => embed.request<{ items: PelicanFacet[] }>('/filters')
}
