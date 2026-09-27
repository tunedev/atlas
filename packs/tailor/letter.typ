// Renders the letter and answers: only sentences that kept a checked
// citation; every cut sentence and unsupported answer is listed as a gap.
#set page(paper: "a4", margin: 2.2cm)
#set text(size: 11pt, hyphenate: false)

#let t = data.tailored

#for s in t.letter.filter(s => not s.gap) [ #s.text ]

#let answers = t.at("answers", default: ())
#if answers.len() > 0 [
  == Questions
  #for a in answers [
    === #a.question
    #let kept = a.sentences.filter(s => not s.gap)
    #if kept.len() == 0 [ _Not shown by the record._ ] else [ #for s in kept [ #s.text ] ]
  ]
]

#let cut = t.letter.filter(s => s.gap)
#if cut.len() > 0 [
  == Removed for lack of evidence
  #for s in cut [ - #s.text ]
]
