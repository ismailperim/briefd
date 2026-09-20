# Team knowledge

This repository is served to coding agents by [briefd](https://github.com/ismailperim/briefd).
Write in whatever language your team thinks in — retrieval is multilingual.

```
domain/          shared: terminology, business rules, architecture decisions
conventions/     shared: coding standards, infra patterns, process
projects/<name>/ only visible when an agent asks for scope "projects/<name>"
```

Rules of thumb (they make retrieval better):

- One idea per `##` section — sections are the unit briefd retrieves and quotes.
- Lead with the rule, then the reason. Concrete numbers, not "reasonable".
- Keep a glossary (`domain/glossary.md`) with your terms and their aliases in
  other languages; agents ask in the words they have.
- Change knowledge through pull requests; agents can propose but never write.
