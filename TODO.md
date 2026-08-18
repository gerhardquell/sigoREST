# TODO 20260818

## Überarbeiten

Wir wollen sigoREST überarbeiten, wenn es erforderlich ist. Mir
ist aufgefallen, daß die apis stoppen, wenn wir Anfragen zu schnell
schicken. Ich schlage vor, daß wir 1/2 - 1sec zwischen folgenden 
Abfragen warten. Wahrscheinlich wird die Gesamtperformance schneller.

## Testumgebung

Lass uns eine entsprechende Testumgebung konstruieren. 
