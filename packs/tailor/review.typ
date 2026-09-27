// Renders the review sheet: everything tailoring could not support, for the
// person sending the documents, never for the reader. data is bound by
// render.run: {"tailored": <claims.settle fields>, "dropped":
// {"requirements", "questions"}} (items.cite and items.gather's dropped
// counts: items asked for that were not verbatim in their source).
#set page(paper: "a4", margin: 2cm)
#set text(size: 10pt, hyphenate: false)

#let t = data.tailored
#let l = t.letter
#let gaps(ss) = ss.filter(s => s.gap)
#let unshown = gaps(t.requirements)
#let removed = gaps(l.sentences).filter(s => s.text != "")
#let unanswered = l.answers.filter(a => a.sentences.all(s => s.gap))
#let unsupported = l.answers.map(a => gaps(a.sentences).filter(s => s.text != "")).flatten()
#let bullets = gaps(t.bullets).len()

= Review before sending

#if unshown.len() > 0 [
  == Requirements not shown by the record
  #for r in unshown [ - #r.text ]
]

#if removed.len() > 0 [
  == Letter sentences removed for lack of evidence
  #for s in removed [ - #s.text ]
]

#if unanswered.len() > 0 [
  == Questions not answered by the record
  #for a in unanswered [ - #a.item ]
]

#if unsupported.len() > 0 [
  == Answer sentences removed for lack of evidence
  #for s in unsupported [ - #s.text ]
]

== Counts
- Requirements dropped as not verbatim in the posting: #data.dropped.requirements
- Questions dropped as not verbatim in the questions asked: #data.dropped.questions
- Bullets dropped for lack of evidence: #bullets
