// Renders the tailored document's structure and bullets. data is bound by
// render.run: {"history": <profile/history.json>, "tailored": <claims.settle fields>}.
#set page(paper: "a4", margin: 2cm)
#set text(size: 10pt, hyphenate: false)

#let t = data.tailored
#let entries = data.history.entries.filter(e => e.at("status", default: "") == "grounded")

= Experience
#for e in entries [
  == #e.title, #e.employer
  #e.start – #if e.at("end", default: none) == none [present] else [#e.end]
]

== Selected work
#for b in t.bullets.filter(b => not b.gap) [
  #for c in b.citations [ - #c.quote ]
]

#let gaps = t.requirements.filter(r => r.gap)
#if gaps.len() > 0 [
  == Not shown by the record
  #for r in gaps [ - #r.text ]
]
