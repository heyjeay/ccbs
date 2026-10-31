package ui

// Theme mirrors btop's "Default" theme keys.
type Theme struct {
	MainBG, MainFG, Title, HiFG       Color
	SelectedBG, SelectedFG            Color
	InactiveFG, GraphText, MeterBG    Color
	DivLine, ProcMisc                 Color
	BoxFive, BoxWeek, BoxNow, BoxHist Color
	GradStart, GradMid, GradEnd       Color // usage / heat
	GraphStart, GraphMid, GraphEnd    Color // token graph
	WeekStart, WeekMid, WeekEnd       Color
	Ok, Warn, Bad                     Color
}

var BtopDefault = Theme{
	MainBG:     Hex("#000000"),
	MainFG:     Hex("cc"),
	Title:      Hex("ee"),
	HiFG:       Hex("#b54040"),
	SelectedBG: Hex("#6a2f2f"),
	SelectedFG: Hex("ee"),
	InactiveFG: Hex("40"),
	GraphText:  Hex("60"),
	MeterBG:    Hex("40"),
	DivLine:    Hex("30"),
	ProcMisc:   Hex("#0de756"),
	BoxFive:    Hex("#556d59"),
	BoxWeek:    Hex("#6c6c4b"),
	BoxNow:     Hex("#5c588d"),
	BoxHist:    Hex("#805252"),
	GradStart:  Hex("#4897d4"),
	GradMid:    Hex("#5474e8"),
	GradEnd:    Hex("#ff40b6"),
	GraphStart: Hex("#77ca9b"),
	GraphMid:   Hex("#cbc06c"),
	GraphEnd:   Hex("#dc4c4c"),
	WeekStart:  Hex("#ffa6d9"),
	WeekMid:    Hex("#dcacd9"),
	WeekEnd:    Hex("#ab8cff"),
	Ok:         Hex("#77ca9b"),
	Warn:       Hex("#cbc06c"),
	Bad:        Hex("#dc4c4c"),
}

// Heat colors a 0..1 utilization green→yellow→red.
func (t Theme) Heat(v float64) Color { return Gradient3(t.GraphStart, t.GraphMid, t.GraphEnd, v) }
