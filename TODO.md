# TODO 20260818

## Überarbeiten

Wir wollen sigoREST überarbeiten, wenn es erforderlich ist. Mir
ist aufgefallen, daß die apis stoppen, wenn wir Anfragen zu schnell
schicken. Ich schlage vor, daß wir 1/2 - 1sec zwischen folgenden 
Abfragen warten. Wahrscheinlich wird die Gesamtperformance schneller.

✅ Erledigt: Pro-Kanal Rate-Limiter (hybrid) implementiert, deployed
   und live gegen echten ZAI-Provider bewiesen. Provider-spezifische
   Werte in channels.json (Mammoth 800ms, Moonshot 500ms, ZAI 400ms).
   Siehe Commits 5b649e3, d6310e2, e6a24b9 + Retrospektive 2026-08-18.

## Testumgebung

Lass uns eine entsprechende Testumgebung konstruieren. 

✅ Erledigt: Mock-Provider in test/mockprovider/ (OpenAI-kompatibel,
   Fixed-Window-Limit) + standalone Binary test/cmd/mockprovider/.
   Integrationstest beweist Limiter schützt Mock vor 429.
