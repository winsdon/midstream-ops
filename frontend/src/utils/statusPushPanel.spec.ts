import { describe, expect, it } from 'vitest'

/**
 * StatusPushPanel 的保存前置条件。
 *
 * 【为什么要单独锁这一条】面板的状态自持在组件里，父组件 Settings.vue 的「保存配置」
 * 按钮在 tab 内容之外、始终可点：切到「渠道推送」tab 前，`info` 仍是 null、
 * settings 还是 defaultSettings()。若此时把默认值 PUT 上去，后端已保存的推送配置
 * 会被整体覆盖成默认值（enabled 变 false，定时推送被关掉），而界面还弹「配置已保存」。
 *
 * 组件里的守卫是 `if (!info.value) throw`。这里用源码断言把它钉住：
 * 面板没有导出的纯函数可以直接断言这条行为，而引入 @vue/test-utils 只为这一个用例
 * 会新增一个依赖。断言源码文本比不测强 —— 有人删掉守卫时这个用例会红。
 */
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const panelSource = readFileSync(
  resolve(here, '../components/settings/StatusPushPanel.vue'),
  'utf8'
)

describe('StatusPushPanel 保存守卫', () => {
  it('未成功加载过时拒绝保存（防止用默认值覆盖远端配置）', () => {
    const saveBody = panelSource.slice(panelSource.indexOf('async function save()'))
    const body = saveBody.slice(0, saveBody.indexOf('\n}'))

    expect(body).toContain('if (!info.value)')
    // 必须是 throw：父组件靠 catch 显示错误并跳过「已保存」提示。
    // 若这里只是 return / showError，父组件仍会弹「配置已保存」，用户以为存上了。
    expect(body).toMatch(/if \(!info\.value\)\s*\{\s*throw/)
  })

  it('挂载时主动加载数据（否则 loading 永远是 true，面板停在「加载中」）', () => {
    expect(panelSource).toMatch(/import \{[^}]*onMounted[^}]*\} from 'vue'/)

    // 挂载钩子里必须真的调到 load()。取 onMounted( 之后一小段源码来判，
    // 不自己解析括号配对 —— 那只会让断言更脆。
    const idx = panelSource.indexOf('onMounted(')
    expect(idx).toBeGreaterThan(-1)
    expect(panelSource.slice(idx, idx + 160)).toMatch(/load\(\)/)
  })

  it('预览传的是表单当前值（未保存的窗口/排除改动也要生效）', () => {
    const previewBody = panelSource.slice(panelSource.indexOf('async function loadPreview()'))
    const body = previewBody.slice(0, previewBody.indexOf('\n}'))
    expect(body).toContain('windowMinutes')
    expect(body).toContain('excludedGroupIds')
  })
})
