// Renders the CV: structure from grounded history entries and the kept
// bullets only; gaps are listed on the review sheet, never here. data is
// bound by render.run: {"history": <profile/history.json>, "tailored":
// <claims.settle fields>}.
#set page(paper: "a4", margin: 2cm)
#set text(size: 10pt, hyphenate: false)

#let t = data.tailored
#let entries = data.history.entries.filter(e => e.at("status", default: "") == "grounded")

#let dates(e) = {
  let end = e.at("end", default: none)
  if e.at("ongoing", default: false) == true [#e.start – present]
  else if end == none [#e.start]
  else [#e.start – #end]
}

= Experience
#for e in entries [
  == #e.title, #e.employer
  #dates(e)
]

== Selected work
#for b in t.bullets.filter(b => not b.gap) [
  #for c in b.citations [ - #c.quote ]
]
