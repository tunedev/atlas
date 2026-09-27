// Renders the letter and the answers: only sentences that kept a checked
// citation, and only questions with at least one; gaps are listed on the
// review sheet, never here. data is bound by render.run: {"tailored":
// <claims.settle fields>}, where tailored.letter is {"sentences", "answers"}.
#set page(paper: "a4", margin: 2.2cm)
#set text(size: 11pt, hyphenate: false)

#let l = data.tailored.letter
#let kept(ss) = ss.filter(s => not s.gap)

#for s in kept(l.sentences) [ #s.text ]

#let answered = l.answers.filter(a => kept(a.sentences).len() > 0)
#if answered.len() > 0 [
  == Answers to the questions asked
  #for a in answered [
    === #a.item
    #for s in kept(a.sentences) [ #s.text ]
  ]
]
