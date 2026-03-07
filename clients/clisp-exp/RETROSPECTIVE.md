# Retrospektive: Experimenteller Common Lisp Client

## Was haben wir gebaut?

Experimenteller Common Lisp Client für sigoREST - funktional, minimalistisch.

## Was musste zusammenkommen?

### 1. Libraries-Stack (4 Pakete)

- `drakma` - HTTP Client (GET/POST/PUT)
- `yason` - JSON Parsing/Encoding
- `babel` - String ↔ Octets Konvertierung (UTF-8)
- `asdf` - System-Management (standardmäßig in SBCL)

### 2. JSON ↔ Lisp Mapping

#### Problem 1: Keywords vs Strings

```lisp
;; Yason parsed JSON Keys als Strings default
:yason-object-as :alist :object-key-fn #'intern-keyword

;; Konvertierung notwendig
(defun intern-keyword (name)
  (intern (string-upcase name) :keyword))
```

#### Problem 2: Hash-Tables für Encoding

```lisp
;; Yason encodiert Hash-Tables zu JSON Objects
(make-hash-table :test 'equal)  ; :test 'equal wichtig für String-Keys!
(setf (gethash "role" msg) "user")
```

#### Problem 3: Arrays vs Listen

```lisp
;; JSON Arrays werden von Yason als Lisp-Listen geparst!
;; Nicht als Vektoren (aref funktioniert nicht)
(car choices)  ; statt (aref choices 0)
```

### 3. HTTP Request-Body Encoding

**Der Knackpunkt:**

```lisp
;; ❌ Funktioniert NICHT - gibt leeren String
(yason:with-output-to-string* () (yason:encode data))

;; ✅ Funktioniert - Standard Common Lisp
(with-output-to-string (s) (yason:encode data s))
```

**Warum?** `yason:with-output-to-string*` scheint einen Bug oder unerwartetes Verhalten zu haben. Die Standard-Lösung `with-output-to-string` funktioniert zuverlässig.

### 4. String ↔ Octets Konvertierung

Drakma braucht Bytes für den Body:

```lisp
(let ((json-bytes (babel:string-to-octets json-string :encoding :utf-8)))
  (drakma:http-request url
                       :method :post
                       :content json-bytes
                       :content-type "application/json; charset=utf-8"))
```

### 5. Package-Management

```lisp
#-asdf (require :asdf)  ; Nur laden wenn ASDF nicht bereits da
(use-package :sigoclient)
```

## Lernpunkte

| Problem | Lösung |
|---------|--------|
| `with-output-to-string*` leer | Standard `with-output-to-string` verwenden |
| JSON Objekte nicht als Hash-Tables | Mit `:object-as :alist` parsen |
| Array-Zugriff mit `aref` fehlgeschlagen | Listen verwenden `(car choices)` |
| Request-Body wird nicht gesendet | In Bytes konvertieren mit babel |
| Keywords nicht gefunden | `intern-keyword` für Key-Konvertierung |

## Vergleich zu anderen Clients

| Feature | Python | Go | JavaScript | CLISP |
|---------|--------|-----|------------|-------|
| Code-Zeilen | ~200 | ~250 | ~180 | ~130 |
| Dependencies | requests | net/http | fetch | drakma+yason+babel |
| Strukturen | Klassen | Structs | Klassen | ❌ (einfache Funktionen) |
| Error Handling | Exceptions | error return | try/catch | handler-case |

## Fazit

Der CLISP Client ist mit ~130 Zeilen der kompakteste. Die Herausforderung war nicht die Logik, sondern das Zusammenspiel der Libraries - besonders Yasons Verhalten war überraschend.

**Würde ich es wieder so machen?**

- Ja, aber ich würde sofort `with-output-to-string` testen statt `with-output-to-string*`
- Die Hash-Table/ALIST-Wahl für Encoding/Decoding ist idiomatic für Lisp

---

*Lisp forever!* 🧙‍♂️
