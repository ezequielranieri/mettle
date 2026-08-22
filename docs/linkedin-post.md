# LinkedIn Post: Mettle

---

**Everyone is building LLM agents. Almost no one is evaluating them seriously.**

I was one of them — until I found a silent restriction bug that two different models reproduced across two different providers.

That's when I built **mettle**: an open-source framework (Go, 100% free stack) that evaluates LLM agents systematically against declared oracles.

---

**What it does:**

→ Declares expected behavior in YAML specs (the oracle)
→ Runs scenario × config matrices against real agents
→ A semantic judge evaluates if the agent met the oracle
→ Persists every run to detect regressions over time

---

**What I found when I used it:**

**1. The defect wasn't in the model — it was in the ecosystem**
Two models (Groq compound-mini, Nvidia nemotron), same failure: restricts access without leaving evidence. Over-conservatism is a pattern, not a bug.

**2. LLM judges disagree**
One judge said FAIL ("hallucination by omission"). Another said PASS — noting the same findings but not concluding the contradiction. A lax judge isn't a good judge.

**3. Defense in depth works**
In one run, the agent was exploited: indirect injection made it call a forbidden tool. The deterministic oracle caught it before any judge intervened. And yes — the same scenario passed in another run. LLMs are stochastic.

---

**The numbers:**

- 12 packages, all tests passing
- 23 Architecture Decision Records
- 13 scenarios across 4 security classes
- 6 providers supported (Groq, Gemini, Cerebras, SambaNova, OpenRouter, Ollama)
- Cost: $0 (free tier)

---

**What I learned:**

1. "I tried it and it looks good" is not evaluation
2. Fail-closed without logging is indistinguishable from a bug
3. Documenting decisions (ADRs) is more valuable than documenting code
4. The eval framework is the content engine — each run produces empirical evidence

---

If you're building LLM agents and want to know what they're actually doing (not what you think they're doing):

⭐ [github.com/ezequielranieri/mettle](https://github.com/ezequielranieri/mettle)

Run it in 5 minutes with a free API key. No credit card required.

---

#LLM #AIEngineering #AISafety #Go #Evals #AgentEvaluation #OpenSource
