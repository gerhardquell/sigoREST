;;;; basic.lisp
;;;; Einfaches Beispiel für den experimentellen sigoclient

;; Stelle sicher, dass ASDF geladen ist
#-asdf (require :asdf)

;; Lade benötigte Systeme
(asdf:load-system :drakma)
(asdf:load-system :yason)
(asdf:load-system :babel)

;; Lade sigoclient
(load "/u/go-projekte/sigoREST/clients/clisp-exp/sigoclient.lisp")

(use-package :sigoclient)

;; Hauptfunktion
(defun main ()
  (format t "🧪 Experimenteller Common Lisp Client für sigoREST~%~%")

  ;; Prüfe Server-Verbindung
  (if (ping)
      (format t "✅ Server erreichbar~%~%")
      (progn
        (format t "❌ Server nicht erreichbar~%")
        (return-from main)))

  ;; Health Check
  (let ((health (health)))
    (format t "Server Status: ~A~%" (cdr (assoc :status health)))
    (format t "Verfügbare Modelle: ~A~%~%" (cdr (assoc :available_models health))))

  ;; Einfacher Chat
  (format t "📝 Chat-Test:~%")
  (format t "Frage: Was ist Quantencomputing?~%~%")

  (let ((antwort (chat "kimi" "Erkläre Quantencomputing in einem Satz. Sei kurz.")))
    (format t "🤖 Antwort: ~A~%~%" antwort))

  ;; Modelle auflisten
  (format t "📋 Erste 5 Modelle:~%")
  (let ((models (list-models)))
    (dotimes (i (min 5 (length models)))
      (let ((model (nth i models)))
        (format t "  - ~A (~A)~%"
                (cdr (assoc :id model))
                (cdr (assoc :shortcode model)))))))

;; Starte
(main)
