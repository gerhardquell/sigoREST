# Aufgabe
sigoREST soll erweitert werden um die Möglichkeit parallel
KIs über eigene Kanäle anzusprechen. Diese Kanäle sollen auch 
ihre eigenen Memorys haben. 
Dazu werden in der env Datei die API_KEYs ausgelesen. 
Beispiel:
MAMMOUTH_API_KEY=sk-O..
MAMMOUTH_API_KEY_0=sk-0...
MAMMOUTH_API_KEY_1=sk-s...
...

Von MAMMOUTH_API_KEY gehen wir aus, die folgenden Keys werden 
hochgezählt. Gleiches gilt für ZAI_API_KEY, MOONSHOT_API_KEY,
usw, also für alle API_KEYs.

Wenn wir parallele Aufgaben zu bewältigen haben, sollen Kanäle 
bei Bedarf zugeschaltet werden. Standardmässig sollen nur die 
nicht numerierten Kanäle aktiviert werden. Die Kanäle sollen
manuell aktivierbar sein. Als Fallback sollen diese Kanäle auch
überwacht werden und notfalls automatisch zugeschaltet werden.

Für jede Session soll ein eigener Memory erzeugt werden, der
auch in einem eigenen Verzeichnis /var/sigoREST gespeichert
wird.

