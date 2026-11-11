# Model Licensing

An agent runs on whichever model its administrator names in `Agent.spec.llm.model`.
That name carries a licence, and some of those licences restrict what the deployment
may be used *for* — not just what may be done with the model file. The restriction can
bite without anyone changing code, so it belongs to the administrator's decisions, the
same as token budgets and egress.

This page is where to look before pointing an agent at a model, and where the
responsibility sits.

---

## 1. What AInsel ships, and what it does not

The platform contains no model weights. Every provider is a hosted endpoint —
`ollama-cloud`, `opencode`, `alibaba-cloud`, or `custom` with a base URL — and the
operator's job is to write the agent's generated pi `models.json` and point it there.
Consequences:

- **Apache-2.0 covers AInsel's code, and nothing else.** It grants nothing about any
  model, and it asserts nothing about any model either.
- **The model licence binds whoever uses the model.** In a self-hosted installation
  that is the organisation running it, not this project. A deployment is its own
  licensee.
- **Because no artifact here redistributes weights, this project carries no model
  attribution duty** — and correspondingly cannot vouch for any model's terms. Read
  the text of what you point an agent at.

## 2. Three questions to read off the licence

| Question | What it reads like | Why it matters for a deployment |
|---|---|---|
| Does it restrict **use by business type**? | "if you offer model-as-a-service, or an AI coding/office assistant, obtain a separate licence" | The trap for a platform whose whole purpose is engineering productivity: an administrator can be in breach by enabling a model, with no code change and no failing test |
| Does it restrict **outputs**? | terms on what may be done with generated content | The only one that *travels*: it attaches to code an agent commits, so it is the question to answer before generated code is published or upstreamed anywhere |
| Does it impose an **attribution threshold**? | "display the model name in the UI above N users / $N revenue" | A deployment can cross it by growing, while changing nothing |

Two more, both easy to miss:

- **Fine-tunes and derivatives inherit.** A LoRA trained on a restricted model is not
  cleaner than the model it came from.
- **"Internal use" usually excludes outputs.** Where such a licence exempts internal
  use, the exemption typically conditions it on the outputs not being made available to
  third parties — publishing agent-generated code to a public repository is exactly
  that. "We only use it internally" is not the safe answer it looks like.

## 3. Behaviour restriction vs artefact restriction

Most provider contracts restrict what *you may do with the service*: no accessing it to
build a competing product, no reselling it, no feeding outputs into training a
competing model. Those are obligations on the operator. The generated code is
yours, and nothing about it follows the code downstream.

A field-of-use clause instead restricts what the *artefact* may be used for. That
restriction attaches to the output, so every later recipient inherits the same
question — which is why an AI-generated contribution to a downstream open source
project has to be checked on this axis specifically.

The distinction maps onto AInsel's two commonest intents:

- an agent commenting on a pull request in your forge — **using the service**;
- code an agent wrote being published, shipped, or upstreamed — **the artefact**.

## 4. Examples to check, not answers to copy

Recorded 2026-11-08 against the texts then published. These documents change without
notifying anyone; treat this table as a list of things to go and read, never as a
finding about the model in front of you.

| Model family | Licence shape | What to go and read |
|---|---|---|
| Qwen community releases (e.g. `Qwen3.8-Flash-Next`) | Qwen Community License 1.0 | Whether you or an affiliate run a model-serving or AI coding/office assistant business — that needs a separate licence for commercial use, derivatives included; the UI-name threshold (MAU / monthly revenue); what the internal-use exemption does with outputs |
| Qwen permissive releases (e.g. `Qwen3-Coder-Next`) | Apache-2.0 | Nothing above — when a permissive release of the same model exists, taking it removes the question entirely |
| Llama releases | Llama Community License | An acceptable-use policy and a large-deployment approval threshold; confirm both in the current text |
| Gemma releases | Gemma Terms of Use | Acceptable-use restrictions |
| Mistral, selected releases | non-production licence variants | Whether production and commercial use are permitted at all |
| Hosted APIs (Anthropic, OpenAI, and similar) | provider terms + usage policy | Whether any clause reaches outputs, not only whether it restricts service use |

## 5. What AInsel enforces — and what it deliberately does not

Dependency licences are policed. A dependency arriving through a routine bump has to
clear a deny-by-default list (`scripts/check-license-policy.py`, run by
`ci-licenses.yml`), because the distributed artifacts redistribute dependencies.

Model licences are not policed, and the operator will not refuse to start an agent
whose model is restricted. That asymmetry is deliberate: AInsel redistributes
dependencies, so a bad one becomes this project's distribution problem; it does not
redistribute models, so the licence stays with the deployment that chose the model.
Moving that decision into the operator would be a false comfort — the operator cannot
read a model's terms, and a webhook cannot judge a business model.

The cost is real, though: nothing in the deployment records *why* a model was chosen,
or that anyone asked. Until that has a home in the API, record the decision where it
outlives the person who made it — the agent's `description`, visible in the console,
or your own asset inventory, with the licence and the date it was checked.

## 6. Deciding for a model nobody has checked

1. Find the licence text, from the model card or the repository's `LICENSE` file — not
   from a blog post about the release, and not from the model's popularity.
2. Answer the three questions in [section 2](#2-three-questions-to-read-off-the-licence)
   in writing, with the clause quoted or linked.
3. If any answer is yes, say so to whoever owns legal review for the organisation,
   and record who accepted it and when.
4. Add it to the table above (or your internal equivalent) with the date checked, so
   the next administrator reads a decision instead of re-deriving one.

The bar is the one the dependency policy already uses: **a licence is decided, not
assumed.**

---

**See also:** [Administrator Guide](administrator-guide.md) for the role this belongs
to, [CRD Reference](crd-reference.md) for `Agent.spec.llm`, and
[Conventions](conventions.md) for recording provenance on a PR.
