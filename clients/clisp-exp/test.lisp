;;;; test.lisp - Test des experimentellen sigoclient

(require :asdf)
(asdf:load-system :drakma)
(asdf:load-system :yason)
(asdf:load-system :babel)

(load "/u/go-projekte/sigoREST/clients/clisp-exp/sigoclient.lisp")
(use-package :sigoclient)

(format t "=== sigoclient Test ===~%~%")

;; Test Ping
(format t "1. Ping Test:~%")
(format t "   Ping: ~A~%~%" (ping))

;; Test Health
(format t "2. Health Test:~%")
(let ((health (health)))
  (format t "   Status: ~A~%" (cdr (assoc :status health)))
  (format t "   Modelle: ~A~%~%" (cdr (assoc :available_models health))))

;; Test Chat
(format t "3. Chat Test (kimi):~%")
(let ((response (chat "kimi" "Hallo!")))
  (format t "   Antwort: ~A~%~%" response))

;; Test List Models
(format t "4. List Models (erste 3):~%")
(let ((models (list-models)))
  (dotimes (i (min 3 (length models)))
    (let ((model (nth i models)))
      (format t "   - ~A (~A)~%"
              (cdr (assoc :id model))
              (cdr (assoc :shortcode model))))))

(format t "~%=== Test abgeschlossen ===~%")
