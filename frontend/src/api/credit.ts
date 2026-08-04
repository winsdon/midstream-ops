/**
 * 授信台账 API。对应 router.go 中 auth 组下的 /credit/* 路由。
 *
 * 与 api/index.ts 分文件而非合并：授信是独立业务域，且 index.ts 已近 300 行。
 * 复用同一个 http 实例与 unwrap，身份体系一致（管理端 Bearer）。
 */
import { http, unwrap } from './client'
import type { ApiResponse, PaginatedData } from '@/types'
import type {
  CreditCustomer,
  CreditLedgerEntry,
  CreditSummary,
  CustomerPayload,
  EntryPayload,
  KycPayload,
  KycProfile,
  KycReviewPayload,
  Sub2apiUserOption
} from '@/types/credit'

export interface CustomerListParams {
  /** 模糊匹配 sub2api_user_id / display_name / email */
  keyword?: string
  /** 空 = 全部状态 */
  status?: string
  /**
   * 排序列。后端白名单校验（customerSortCols），非法值静默回退按敞口降序。
   * 这张表走后端排序而非前端本地排序：它有分页，本地只能排当前页，
   * 「这 20 人里敞口最大的」会被误读成「敞口最大的」，授信决策不能赌这个。
   */
  sort?: string
  order?: 'asc' | 'desc'
  page?: number
  page_size?: number
}

export const creditApi = {
  summary: () => unwrap<CreditSummary>(http.get<ApiResponse<CreditSummary>>('/credit/summary')),

  /**
   * 建档下拉数据源（读线上 users 表）。
   * PG 不可用时后端返回 503，调用方须捕获并降级为手填输入框——
   * 线上库挂了就建不了档是不可接受的。
   */
  sub2apiUsers: () =>
    unwrap<{ items: Sub2apiUserOption[] }>(
      http.get<ApiResponse<{ items: Sub2apiUserOption[] }>>('/credit/sub2api-users')
    ),

  listCustomers: (params: CustomerListParams) =>
    unwrap<PaginatedData<CreditCustomer>>(
      http.get<ApiResponse<PaginatedData<CreditCustomer>>>('/credit/customers', { params })
    ),

  createCustomer: (payload: CustomerPayload) =>
    unwrap<CreditCustomer>(http.post<ApiResponse<CreditCustomer>>('/credit/customers', payload)),

  updateCustomer: (id: number, payload: CustomerPayload) =>
    unwrap<CreditCustomer>(http.put<ApiResponse<CreditCustomer>>(`/credit/customers/${id}`, payload)),

  /** 归档而非物理删除，台账记录完整保留 */
  archiveCustomer: (id: number) =>
    unwrap<{ archived: boolean }>(http.delete<ApiResponse<{ archived: boolean }>>(`/credit/customers/${id}`)),

  /** 按台账全量重算单客户敞口（幂等） */
  recalc: (id: number) =>
    unwrap<CreditCustomer>(http.post<ApiResponse<CreditCustomer>>(`/credit/customers/${id}/recalc`)),

  /** 全量重算兜底，返回受影响客户数 */
  recalcAll: () =>
    unwrap<{ recalculated: number }>(http.post<ApiResponse<{ recalculated: number }>>('/credit/recalc')),

  listLedger: (id: number, page = 1, pageSize = 20) =>
    unwrap<PaginatedData<CreditLedgerEntry>>(
      http.get<ApiResponse<PaginatedData<CreditLedgerEntry>>>(`/credit/customers/${id}/ledger`, {
        params: { page, page_size: pageSize }
      })
    ),

  /** 记一笔垫付/回款，返回记账后的客户（含最新敞口） */
  appendEntry: (id: number, payload: EntryPayload) =>
    unwrap<CreditCustomer>(http.post<ApiResponse<CreditCustomer>>(`/credit/customers/${id}/ledger`, payload)),

  /** 冲正：写一笔等额反向分录，原分录保留。同一分录只能冲正一次 */
  reverseEntry: (entryId: number) =>
    unwrap<CreditCustomer>(http.post<ApiResponse<CreditCustomer>>(`/credit/ledger/${entryId}/reverse`)),

  /**
   * 读取 KYC 资料。客户尚未录入时后端返回一份空白档案（status=draft）而非 404，
   * 前端可直接绑定表单；只有客户本身不存在才会 404。
   */
  getKyc: (customerId: number) =>
    unwrap<KycProfile>(http.get<ApiResponse<KycProfile>>(`/credit/customers/${customerId}/kyc`)),

  /** 保存 KYC。payload.submit=true 时后端会校验必填并置为 pending */
  saveKyc: (customerId: number, payload: KycPayload) =>
    unwrap<KycProfile>(http.put<ApiResponse<KycProfile>>(`/credit/customers/${customerId}/kyc`, payload)),

  /** 审核。审核人取自登录会话，不由前端传 */
  reviewKyc: (customerId: number, payload: KycReviewPayload) =>
    unwrap<KycProfile>(
      http.post<ApiResponse<KycProfile>>(`/credit/customers/${customerId}/kyc/review`, payload)
    )
}
