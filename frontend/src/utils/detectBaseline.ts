/** 基准模型与检测模型是否视为同一模型（去空白、忽略大小写）。 */
export function sameBaselineModel(baseline: string, actual: string): boolean {
  return baseline.trim().toLowerCase() === actual.trim().toLowerCase()
}

/** 已保存基准是否应对本轮检测生效。 */
export function baselineApplies(
  baseline: { quality_ok?: boolean; model?: string } | null | undefined,
  testModel: string
): boolean {
  if (!baseline?.quality_ok) return false
  return sameBaselineModel(baseline.model ?? '', testModel)
}
