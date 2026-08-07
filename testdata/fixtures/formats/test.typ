// An article in the shape of Typst's starter templates.
#import "@preview/charged-ieee:0.1.3": ieee

#set page(paper: "us-letter", numbering: "1")
#set text(font: "New Computer Modern", size: 10pt)
#set heading(numbering: "1.1")
#show link: set text(fill: blue)

#let remark(body) = [*Remark:* #body]

#align(center, text(17pt)[
  A centered title with a TODO in it
])

= Introduction <sec-intro-NOTE>

Body prose with a TODO in it, alongside _emphasis with an XXX in it_ and
*strong prose with a TODO in it* and `inline raw with FIXME` spans.

As @sec-intro-NOTE shows, references are markup, and a label <lbl-FIXME>
says nothing either.

// A line comment with a FIXME in it.

/* A block comment,
   nested /* further */, with a NOTE in it. */

== Methods

Inline math $E = m c^2$ is skipped, and so is display math:

$ sum_(k=1)^n k = (n (n+1)) / 2 $

#figure(
  image("diagram-FIXME.png", width: 70%),
  caption: [A figure caption with a TODO in it.],
) <fig-diagram>

The total is #calc.round(1.234, digits: 2) units, and #remark[a remark
argument with an XXX in it] flows inline.

- A list item with a TODO in it.
- A second item.

+ A numbered item with an XXX in it.
+ Another numbered item.

/ Precision: A term description with a TODO in it.

```python
x = 1  # a FIXME in a code block
```

#if true [
  Conditional prose with an XXX in it.
]

Escaped \*markers\* stay literal, a~tie is a space, and a final XXX ends it.
