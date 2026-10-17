#!/usr/bin/env python3
"""把 OFL 授权的中文字体裁剪成子集字体，供 statusimg 包 go:embed。

【为什么要裁剪】statusimg 把字体直接嵌进 Go 二进制（镜像里不保证有中文字体），
而完整的 NotoSansSC-Regular.otf 有 8.3MB；看板用到的汉字（渠道名、状态词、
成功/缓存/暂无数据等界面文案）全部落在 GB2312 之内，裁到「GB2312 全集 +
ASCII + 常用标点」后体积大幅下降，且字形零损失（是删表项，不是转曲重排）。

【字体来源与授权】Noto Sans SC，SIL Open Font License 1.1，允许随软件分发
（含嵌入二进制）；OFL 全文随字体放在 backend/internal/statusimg/fonts/OFL.txt。
官方源：
  https://github.com/notofonts/noto-cjk/raw/main/Sans/SubsetOTF/SC/NotoSansSC-Regular.otf
GitHub raw 偶发 `curl: (52) Empty reply from server`，可用 jsDelivr 镜像：
  https://cdn.jsdelivr.net/gh/notofonts/noto-cjk@main/Sans/SubsetOTF/SC/NotoSansSC-Regular.otf

【依赖】pip install fonttools

【常用命令】
  python deploy/scripts/subset-font.py \
      --input  .tmp/statusimg-font/NotoSansSC-Regular.otf \
      --output backend/internal/statusimg/fonts/NotoSansSC-Regular.otf

  只想看字符集规模、不落盘：
  python deploy/scripts/subset-font.py --input <font> --output <out> --dry-run
"""

from __future__ import annotations

import argparse
import os
import sys
import tempfile

# GB2312 的编码空间：首字节 0xA1–0xF7，尾字节 0xA1–0xFE。
# 逐对尝试解码（Python 的 gb2312 编解码器会拒绝空洞），得到的才是真正的
# GB2312 字表（6763 个汉字 + 682 个符号），而不是「按区间硬凑」的近似集。
GB2312_LEAD_START, GB2312_LEAD_END = 0xA1, 0xF7
GB2312_TRAIL_START, GB2312_TRAIL_END = 0xA1, 0xFE

# ASCII 可打印字符：状态文案里的数字、百分号、x（倍率）、括号都在这里。
ASCII_PRINTABLE = "".join(chr(c) for c in range(0x20, 0x7F))

# 常用标点补充：GB2312 已覆盖绝大多数，这里再钉一遍是防止「换字体后符号消失」
# —— 省略号「…」是截断逻辑的产物，缺了就变成豆腐块，必须在子集里。
COMMON_PUNCTUATION = (
    "·—…–‘’“”「」『』（）〈〉《》【】〔〕、。，；：？！"
    "～￥＄％‰°′″±×÷≈≤≥≠∞→←↑↓√"
)


def gb2312_charset() -> str:
    """返回 GB2312 全集（含其符号区）。"""
    chars: list[str] = []
    for lead in range(GB2312_LEAD_START, GB2312_LEAD_END + 1):
        for trail in range(GB2312_TRAIL_START, GB2312_TRAIL_END + 1):
            try:
                chars.append(bytes((lead, trail)).decode("gb2312"))
            except UnicodeDecodeError:
                # GB2312 的码位空间有空洞，解码失败即该码位未定义。
                continue
    return "".join(chars)


def build_charset(args: argparse.Namespace) -> str:
    """按 --charset 选定的来源汇总字符集。"""
    if args.charset == "ascii":
        chunks = [ASCII_PRINTABLE]
    elif args.charset == "custom":
        # 完全自定义：只用 --charset-file / --text 指定的字符，方便做极小字体。
        chunks = []
    else:  # gb2312（默认）
        chunks = [gb2312_charset(), ASCII_PRINTABLE, COMMON_PUNCTUATION]

    for path in args.charset_file or []:
        with open(path, encoding="utf-8") as fh:
            chunks.append(fh.read())

    if args.text:
        chunks.append(args.text)

    # 去重且排序：字符集内容稳定，重复执行脚本产出的字体字节也稳定。
    merged = set("".join(chunks))
    merged.discard("\n")
    merged.discard("\r")
    merged.discard("\t")
    return "".join(sorted(merged))


def report_missing(ttfont, charset: str) -> list[str]:
    """报告源字体里没有的字符 —— 子集化不会报错，只会静默丢字，必须显式核对。"""
    cmap = ttfont.getBestCmap()
    return [ch for ch in charset if ord(ch) not in cmap]


def download(url: str, dst: str) -> None:
    import urllib.request

    print(f"[subset-font] 下载 {url}")
    with urllib.request.urlopen(url, timeout=180) as resp, open(dst, "wb") as out:
        out.write(resp.read())


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="裁剪中文字体为子集（GB2312 全集 + ASCII + 常用标点）",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--input", required=True, help="源字体路径（TTF/OTF）")
    parser.add_argument("--output", required=True, help="输出子集字体路径")
    parser.add_argument(
        "--charset",
        choices=("gb2312", "ascii", "custom"),
        default="gb2312",
        help="字符集来源：gb2312=GB2312全集+ASCII+常用标点（默认）；"
        "ascii=仅 ASCII；custom=仅 --charset-file/--text 指定的字符",
    )
    parser.add_argument(
        "--charset-file",
        action="append",
        metavar="PATH",
        help="额外字符来源文件（UTF-8，取其中每个非空白字符）；可重复",
    )
    parser.add_argument("--text", help="额外字符（直接写在命令行里）")
    parser.add_argument(
        "--keep-hinting",
        action="store_true",
        help="保留 hinting；默认丢弃（Go 侧 x/image 在 2× 渲染下不使用 hinting）",
    )
    parser.add_argument(
        "--flavor",
        choices=("none", "woff2"),
        default="none",
        help="输出包装格式；go:embed 用 none（原始 TTF/OTF）",
    )
    parser.add_argument(
        "--download",
        metavar="URL",
        help="当 --input 不存在时，先从这个 URL 下载源字体",
    )
    parser.add_argument("--dry-run", action="store_true", help="只打印字符集统计，不产出文件")
    args = parser.parse_args(argv)

    if not os.path.exists(args.input):
        if args.download:
            os.makedirs(os.path.dirname(os.path.abspath(args.input)), exist_ok=True)
            download(args.download, args.input)
        else:
            print(f"[subset-font] 源字体不存在：{args.input}", file=sys.stderr)
            return 1

    try:
        from fontTools import subset
        from fontTools.ttLib import TTFont
    except ImportError:
        print("[subset-font] 缺少 fontTools，请先执行：pip install fonttools", file=sys.stderr)
        return 1

    charset = build_charset(args)
    print(f"[subset-font] 字符集来源={args.charset} 字符数={len(charset)}")

    source = TTFont(args.input, lazy=True)
    missing = report_missing(source, charset)
    print(f"[subset-font] 源字体缺失字符数={len(missing)}")
    if missing:
        # 不直接失败：缺字只影响这几百个字符，露出明细让人判断是否换了字体。
        preview = "".join(missing[:40])
        print(f"[subset-font]   缺失示例：{preview}", file=sys.stderr)

    if args.dry_run:
        return 0

    os.makedirs(os.path.dirname(os.path.abspath(args.output)), exist_ok=True)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", suffix=".txt", delete=False) as tmp:
        tmp.write(charset)
        text_file = tmp.name

    try:
        subset_args = [
            args.input,
            f"--output-file={args.output}",
            f"--text-file={text_file}",
            # 保留布局特性与命名表：前者保证字距/上下标可用，后者保留版权与
            # 字体名信息（OFL 要求分发时保留版权声明）。
            "--layout-features=*",
            "--name-IDs=*",
            "--name-legacy",
            "--name-languages=*",
            # .notdef 与推荐字形：缺字时渲染方框而不是空白，便于发现问题。
            "--notdef-outline",
            "--recommended-glyphs",
            "--drop-tables+=DSIG",
        ]
        if not args.keep_hinting:
            subset_args.append("--no-hinting")
        if args.flavor != "none":
            subset_args.append(f"--flavor={args.flavor}")
        subset.main(subset_args)
    finally:
        os.unlink(text_file)

    src_size = os.path.getsize(args.input)
    dst_size = os.path.getsize(args.output)
    print(
        f"[subset-font] 完成：{args.input} ({src_size / 1024 / 1024:.2f}MB) -> "
        f"{args.output} ({dst_size / 1024 / 1024:.2f}MB)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
