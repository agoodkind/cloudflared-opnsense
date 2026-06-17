[
    .[].tagName
    | select(startswith($prefix))
    | ltrimstr($prefix)
    | select(test("^[0-9]+$"))
    | tonumber
]
| max // empty
