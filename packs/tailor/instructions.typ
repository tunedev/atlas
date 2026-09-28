// Renders the sending instructions: where to submit, which file goes where,
// each supported answer to paste under its question, the review sheet's
// gaps to check first, and the command that declares the send. Atlas sends
// nothing; the person does. data is bound by render.run: {"url",
// "subject_id", "files", "answers": <claims.settle letter.answers>, "gaps":
// <claims.settle gaps_all>}.
#set page(paper: "a4", margin: 2cm)
#set text(size: 10pt, hyphenate: false)

#let kept(ss) = ss.filter(s => not s.gap)
#let answered = data.answers.filter(a => kept(a.sentences).len() > 0)
// url-line sets url on one line, never broken, shrunk to fit width when it
// is wider.
#let url-line(url, width) = {
  let l = link(url)
  let w = measure(l).width
  let r = calc.min(1, width / w)
  box(width: w * r, scale(r * 100%, origin: left, reflow: true, box(width: w, l)))
}
#let where = (
  "cv.pdf": "the CV or résumé upload",
  "letter.pdf": "the cover letter upload, or paste its text where the form asks for one",
)

= Sending #data.subject_id

== Where to submit
#if data.url != "" [ #layout(size => url-line(data.url, size.width)) ] else [ No link in the record: find it on the board. ]

== Which file goes where
#for f in data.files [ - #f: #where.at(f, default: "where the form asks for it") ]

#if answered.len() > 0 [
  == Answers to paste
  #for a in answered [
    === #a.item
    #for s in kept(a.sentences) [ #s.text ]
  ]
]

== Check before sending
#if data.gaps.len() > 0 [
  The review sheet lists these gaps; make sure nothing you send claims them.
  #for g in data.gaps [ - #g ]
] else [ The review sheet lists no gaps. ]

== Once sent
Declare the send, which starts the follow-up clock:

#raw("atlas -pack packs/sent.yaml -var subject_id=" + data.subject_id + " [-var when=<RFC 3339>]", block: true)
