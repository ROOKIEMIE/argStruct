package argStruct

import (
	"os"
	"os/exec"
	run "runtime"
	"strconv"
	"strings"
	"unicode"
)

//
// 终端宽度相关
//

// Windows 下：
// 1) 尝试调用 PowerShell: (Get-Host).UI.RawUI.WindowSize.Width
// 2) 失败则退回到 cmd: mode con
func getTermWidthWindows() int {
	// 尝试 PowerShell
	ps := exec.Command("powershell", "-NoProfile", "-Command", "(Get-Host).UI.RawUI.WindowSize.Width")
	if out, err := ps.Output(); err == nil {
		if v, err2 := strconv.Atoi(strings.TrimSpace(string(out))); err2 == nil && v > 0 {
			return v
		}
	}

	// 再尝试 cmd
	cmd := exec.Command("cmd", "/C", "mode con")
	if out, err := cmd.Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			// 英文环境通常包含 "Columns"
			if strings.Contains(line, "Columns") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					if v, err2 := strconv.Atoi(fields[len(fields)-1]); err2 == nil && v > 0 {
						return v
					}
				}
			}
		}
	}
	return 0
}

// Unix-like 下：
// 1) 优先使用 tput cols
// 2) 失败由上层回退默认值
func getTermWidthUnix() int {
	// tput cols 在大部分 terminfo 环境里都有
	cmd := exec.Command("tput", "cols")
	cmd.Stdin = os.Stdin // 有些终端需要从当前 TTY 读取
	if out, err := cmd.Output(); err == nil {
		if v, err2 := strconv.Atoi(strings.TrimSpace(string(out))); err2 == nil && v > 0 {
			return v
		}
	}
	return 0
}

// GetTermWidth 尽量获取当前终端的列宽，失败时返回 80。
func GetTermWidth() int {
	// 1. 统一优先使用 COLUMNS 环境变量（在 Bash/Zsh/部分终端中很常见）
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if v, err := strconv.Atoi(cols); err == nil && v > 0 {
			return v
		}
	}

	// 2. 根据操作系统分支
	switch run.GOOS {
	case "windows":
		if w := getTermWidthWindows(); w > 0 {
			return w
		}
	default:
		if w := getTermWidthUnix(); w > 0 {
			return w
		}
	}

	// 3. 所有方式失败时的兜底值
	return 80
}

//
// 对齐、宽度工具
//

type Alignment int

const (
	LeftAlign Alignment = iota
	RightAlign
	CenterAlign
)

type Title struct {
	titleContent   string
	titleUnderline string
}

type Header struct {
	headerUnderline string
	headerContent   []*Cell
}

// runeDisplayWidth 计算单个 rune 的显示宽度。
// - ASCII 等普通字符宽度 1
// - CJK（汉字/日文平假名/片假名/韩文）宽度 2
// - 控制字符宽度 0
// - '\t' 简单按 4 宽处理（不精确，但在帮助信息中足够）
func runeDisplayWidth(r rune) int {
	switch {
	case r == '\t':
		return 4
	case r < 0x20:
		// 控制字符不占宽
		return 0
	case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
		return 2
	default:
		return 1
	}
}

// stringDisplayWidth 计算字符串的显示宽度
func stringDisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeDisplayWidth(r)
	}
	return w
}

// padRightByWidth 按“显示宽度”右侧补空格，直到达到 targetWidth。
// 如果 s 的显示宽度 >= targetWidth，则原样返回。
func padRightByWidth(s string, targetWidth int) string {
	if targetWidth <= 0 {
		return s
	}
	w := stringDisplayWidth(s)
	if w >= targetWidth {
		return s
	}
	return s + strings.Repeat(" ", targetWidth-w)
}

// splitByWidth 按显示宽度拆分字符串，使得每一段的显示宽度 <= width。
// 当前实现按字符级别硬拆，不做按空格优先换行。
func splitByWidth(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var segs []string
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runeDisplayWidth(r)
		// 如果当前行已有内容且再加这个 rune 超出宽度，则换行
		if cur > 0 && cur+rw > width {
			segs = append(segs, b.String())
			b.Reset()
			cur = 0
		}
		b.WriteRune(r)
		cur += rw
	}
	if b.Len() > 0 || len(segs) == 0 {
		segs = append(segs, b.String())
	}
	return segs
}

type Cell struct {
	// 对齐方式，默认左对齐
	alignment Alignment
	// minWidth = data 的显示宽度（通过 displayWidth 计算后更新）
	minWidth int
	// maxWidth = minWidth + len(padding)（padding 只包含空格/单宽字符）
	maxWidth int
	// padding default is ' '（右侧补空格）
	padding string
	data    string
}

func NewTableCell(data string) *Cell {
	return &Cell{
		maxWidth: len(data),
		minWidth: len(data),
		data:     data,
	}
}

func (c *Cell) SetAlign(customAlign Alignment) {
	switch customAlign {
	case LeftAlign:
		c.alignment = LeftAlign
	case RightAlign:
		c.alignment = RightAlign
	case CenterAlign:
		c.alignment = CenterAlign
	default:
		c.alignment = LeftAlign
	}
}

// displayWidth 返回当前 Cell.data 的显示宽度，并更新 minWidth/maxWidth。
func (c *Cell) displayWidth() int {
	w := stringDisplayWidth(c.data)
	c.minWidth = w
	// maxWidth 在设置 padding 时再更新
	if c.maxWidth < w {
		c.maxWidth = w
	}
	return w
}

// SetPaddingWithLen 设置右侧 padding 的长度（以空格数量计），并更新 maxWidth。
func (c *Cell) SetPaddingWithLen(paddingLen int) {
	if paddingLen <= 0 {
		c.padding = ""
		c.maxWidth = c.minWidth
		return
	}
	c.padding = strings.Repeat(" ", paddingLen)
	c.maxWidth = c.minWidth + len(c.padding)
}

func (c *Cell) ToString() string {
	switch c.alignment {
	case LeftAlign:
		return c.data + c.padding
	case RightAlign:
		return c.padding + c.data
	case CenterAlign:
		// 暂不支持真正的居中，默认左对齐
		return c.data + c.padding
	default:
		return c.data + c.padding
	}
}

type Row struct {
	// rowWidth 表示该行在终端上可用的最大宽度（不含左侧缩进）。
	// 目前由 Table.updateTermWidth 统一设置，折行逻辑由 Table.ToStrings 负责。
	rowWidth int
	rawCells []*Cell
	// rowStrs 记录该行渲染后的物理行（可能包含折行）；目前主要用于调试或后续扩展。
	rowStrs []string
}

func (r *Row) GetMaxCell() *Cell {
	var maxCell *Cell
	maxWidth := -1
	for _, c := range r.rawCells {
		if c == nil {
			continue
		}
		w := stringDisplayWidth(c.data)
		// 这里用 >=，和原来逻辑保持一致：相同宽度时“靠后的”赢
		if w >= maxWidth {
			maxWidth = w
			maxCell = c
			c.minWidth = w
			if c.maxWidth < w {
				c.maxWidth = w
			}
		}
	}
	return maxCell
}

// JoinCells 将当前行所有 Cell 拼成一行字符串（不考虑折行）。
func (r *Row) JoinCells() string {
	var cellStrs []string
	for _, c := range r.rawCells {
		cellStrs = append(cellStrs, c.ToString())
	}
	return strings.Join(cellStrs, "")
}

// GetRowLen 返回 JoinCells 的字节长度（调试用）。实际显示宽度请使用 stringDisplayWidth。
func (r *Row) GetRowLen() int {
	return len(r.JoinCells())
}

// ToString 将当前逻辑行渲染为单行字符串（不处理折行），并保存到 rowStrs 中。
// 最终折行逻辑由 Table.ToStrings 统一处理。
func (r *Row) ToString() (string, error) {
	singleCellStr := r.JoinCells()
	r.rowStrs = []string{singleCellStr}
	return singleCellStr, nil
}

type Table struct {
	// termWidth 当前终端总宽度（列数）
	termWidth int
	// tableIndent 每行左侧缩进的空格数
	tableIndent int
	// tableTitle 默认无
	tableTitle *Title
	// tableHeader 默认无
	tableHeader *Header
	// rows 每个字段对应一行逻辑行
	rows []*Row
}

// InitRenderTable 创建一个带指定缩进的表格。
func InitRenderTable(indent int) *Table {
	return &Table{tableIndent: indent}
}

func (t *Table) AppendRowWithCells(cells []*Cell) {
	if cells != nil {
		t.rows = append(t.rows, &Row{rawCells: cells})
	}
}

func (t *Table) AppendRow(r *Row) {
	if r != nil {
		t.rows = append(t.rows, r)
	}
}

// InsertRow 在 i+1 的位置插入
func (t *Table) InsertRow(i int, r *Row) {
	if r == nil {
		return
	}
	if i < 0 || i >= len(t.rows) {
		return
	}
	if i+1 == len(t.rows) {
		t.AppendRow(r)
	} else {
		t.rows = append(t.rows[:i+1], append([]*Row{r}, t.rows[i+1:]...)...)
	}
}

// RemoveRowWithIndex 通过下标移除一行
func (t *Table) RemoveRowWithIndex(i int) {
	if i < 0 || i >= len(t.rows) {
		return
	}
	if i+1 == len(t.rows) {
		t.rows = t.rows[:i]
	} else {
		t.rows = append(t.rows[:i], t.rows[i+1:]...)
	}
}

// updateTermWidth 获取/重置当前的终端宽度，并为每行设置 rowWidth。
//
//  1. 如果 t.termWidth 已经被用户显式设置且 >0，则沿用；
//  2. 否则调用 GetTermWidth 获取当前终端宽度；
//  3. 将可用行宽 (termWidth - tableIndent) 填到每个 row 的 rowWidth 中。
func (t *Table) updateTermWidth() {
	width := t.termWidth
	if width <= 0 {
		width = GetTermWidth()
	}
	t.termWidth = width

	usable := width - t.tableIndent
	if usable <= 0 {
		// 防止缩进超过终端宽度导致负数
		usable = width
	}
	for _, r := range t.rows {
		r.rowWidth = usable
	}
}

// chooseWrapColumn 根据当前表格内容选择“折行列”的索引：
// 1) 每行调用 GetMaxCell，找到该 Cell 所在列，为该列投票；
// 2) 票数最多的列作为 wrap 列；
// 3) 如果没有投票（全空行等），退回到选择 colDataWidth 最大的那列。
func (t *Table) chooseWrapColumn(colDataWidth []int, maxCols int) int {
	if maxCols == 0 {
		return 0
	}
	// 单列表格直接选第 0 列
	if maxCols == 1 {
		return 0
	}

	votes := make([]int, maxCols)

	for _, r := range t.rows {
		if len(r.rawCells) == 0 {
			continue
		}
		maxCell := r.GetMaxCell()
		if maxCell == nil {
			continue
		}
		for idx, c := range r.rawCells {
			if c == maxCell {
				if idx >= 0 && idx < maxCols {
					votes[idx]++
				}
				break
			}
		}
	}

	// 根据票数选出 wrap 列
	wrapCol := -1
	bestVotes := -1
	for i, v := range votes {
		if v > bestVotes {
			bestVotes = v
			wrapCol = i
		}
	}

	// 没有任何投票（比如所有 Row 都没有 cell），退回到“最长的列”
	if wrapCol == -1 {
		bestWidth := -1
		for i, w := range colDataWidth {
			if w > bestWidth {
				bestWidth = w
				wrapCol = i
			}
		}
	}

	if wrapCol < 0 {
		wrapCol = 0
	}
	return wrapCol
}

// ToStrings 将表格以行数组的形式返回，支持：
//  1. 列对齐（按显示宽度对齐每一列）；
//  2. 根据实际内容使用 Row.GetMaxCell 在“表级别”选择一列作为折行列，
//     并按终端宽度对这一列做折行。
func (t *Table) ToStrings() []string {
	if len(t.rows) == 0 {
		return nil
	}
	t.updateTermWidth()

	// 1. 计算最大列数（看所有行中最多有多少个 Cell）
	maxCols := 0
	for _, r := range t.rows {
		if n := len(r.rawCells); n > maxCols {
			maxCols = n
		}
	}
	if maxCols == 0 {
		return nil
	}

	// 2. 计算每一列的最大“显示宽”（只看 data，不含 padding/列间距）
	colDataWidth := make([]int, maxCols)
	for _, r := range t.rows {
		for j := 0; j < maxCols; j++ {
			if j >= len(r.rawCells) {
				continue
			}
			w := stringDisplayWidth(r.rawCells[j].data)
			if w > colDataWidth[j] {
				colDataWidth[j] = w
			}
		}
	}

	// 3. 初始列宽 = dataMaxWidth + 列间距
	const colSpacing = 4
	colWidth := make([]int, maxCols)
	for j := 0; j < maxCols; j++ {
		spacing := colSpacing
		// 最后一列一般不再额外加列间距
		if j == maxCols-1 {
			spacing = 0
		}
		colWidth[j] = colDataWidth[j] + spacing
	}

	// 4. 用 Row.GetMaxCell 在“表级别”选出 wrap 列
	wrapCol := t.chooseWrapColumn(colDataWidth, maxCols)

	// 5. 根据终端可用宽度压缩 wrap 列列宽（确保 sum(colWidth) <= usable）
	usable := 0
	if len(t.rows) > 0 {
		usable = t.rows[0].rowWidth
	}
	if usable <= 0 {
		usable = t.termWidth - t.tableIndent
	}
	if usable <= 0 {
		usable = t.termWidth
	}

	totalWidth := 0
	for _, w := range colWidth {
		totalWidth += w
	}

	if totalWidth > usable {
		// 把除 wrapCol 之外的列视为“固定宽度”
		fixed := 0
		for j := 0; j < maxCols; j++ {
			if j == wrapCol {
				continue
			}
			fixed += colWidth[j]
		}

		// 只有在 fixed < usable 时才有意义去压缩 wrap 列
		if fixed < usable {
			avail := usable - fixed
			if avail < 1 {
				avail = 1
			}
			colWidth[wrapCol] = avail
		}
		// 如果 fixed >= usable，就说明其它列已经撑满/超出宽度，
		// 此时再压 wrap 列也不能让整行变短，只会把 wrap 列变成“宽度 1”。
		// 所以这里我们保持 colWidth[wrapCol] 不变，让后续按原始宽度折行或直接溢出。
	}

	indentStr := strings.Repeat(" ", t.tableIndent)
	var lines []string

	// 6. 对每一“逻辑行”构造若干“物理行”（wrapCol 内部折行）
	for _, r := range t.rows {
		if len(r.rawCells) == 0 {
			continue
		}

		// 统一视作 maxCols 列，不足的列当空串
		nCols := maxCols

		// 6.1 构造各列的“首行内容”和“续行空白占位”
		fixedParts := make([]string, nCols)   // 首行：非 wrapCol 列内容
		fixedFillers := make([]string, nCols) // 续行：非 wrapCol 列空白占位

		for j := 0; j < nCols; j++ {
			w := colWidth[j]
			if w < 0 {
				w = 0
			}

			var data string
			if j < len(r.rawCells) {
				data = r.rawCells[j].data
			} else {
				data = ""
			}

			if j == wrapCol {
				// wrap 列内容后面单独处理
				continue
			}

			fixedParts[j] = padRightByWidth(data, w)
			fixedFillers[j] = strings.Repeat(" ", w)
		}

		// 6.2 处理 wrap 列：按“数据宽度”拆分为多行。
		//   数据宽度 = 列宽 - 列间距（对非最后一列保留列间距，避免把列间空格挤没）
		wrapWidth := colWidth[wrapCol]

		// 对非最后一列，预留 colSpacing 作为列间距
		spacingForWrap := 0
		if wrapCol != maxCols-1 {
			spacingForWrap = colSpacing
		}
		if wrapWidth > spacingForWrap {
			wrapWidth = wrapWidth - spacingForWrap
		}

		// 兜底：wrapWidth 至少要是一个正数（并尽量不超过原始数据宽）
		if wrapWidth <= 0 || wrapWidth > colDataWidth[wrapCol] {
			wrapWidth = colDataWidth[wrapCol]
		}
		if wrapWidth <= 0 {
			wrapWidth = 1
		}

		var wrapData string
		if wrapCol < len(r.rawCells) {
			wrapData = r.rawCells[wrapCol].data
		} else {
			wrapData = ""
		}

		segments := splitByWidth(wrapData, wrapWidth)
		if len(segments) == 0 {
			segments = []string{""}
		}

		// 6.3 生成物理行：首行打印全部列，续行只打印 wrap 列，其余列用空白占位
		rowPhysicalLines := make([]string, 0, len(segments))
		for i, seg := range segments {
			var sb strings.Builder
			sb.WriteString(indentStr)

			for j := 0; j < nCols; j++ {
				w := colWidth[j]
				if j == wrapCol {
					// 折行列：每一行都打印对应片段
					sb.WriteString(padRightByWidth(seg, w))
					continue
				}
				// 非折行列：首行打印内容，后续行打印空白占位
				if i == 0 {
					sb.WriteString(fixedParts[j])
				} else {
					sb.WriteString(fixedFillers[j])
				}
			}

			line := sb.String()
			lines = append(lines, line)
			rowPhysicalLines = append(rowPhysicalLines, line)
		}
		r.rowStrs = rowPhysicalLines
	}

	return lines
}

func (t *Table) ToString() string {
	return strings.Join(t.ToStrings(), "\n")
}
