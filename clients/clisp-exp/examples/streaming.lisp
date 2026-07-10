;;;; streaming.lisp
;;;; Demonstriert echtes SSE-Streaming mit dem modernisierten sigoclient

#-asdf (require :asdf)
(asdf:load-system :drakma)
(asdf:load-system :yason)
(asdf:load-system :babel)

(load "/u/go-projekte/sigoREST/clients/clisp-exp/sigoclient.lisp")
(use-package :sigoclient)

(defun main ()
  (format t "~%🌊 sigoclient v2 - Streaming Demo (Common Lisp)~%~%")

  (if (not (ping))
      (progn
        (format t "❌ Server nicht erreichbar~%")
        (return-from main))
      (format t "✅ Server erreichbar~%~%"))

  (let ((model "cl5-s")
        (prompt "Schreibe ein kurzes, schönes Haiku über Common Lisp und KI."))
    
    (format t "👤 Prompt: ~A~%~%" prompt)
    (format t "🤖 Assistant: ")

    (let ((stream (chat-stream model prompt)))
      (when (functionp stream)
        (loop for item = (funcall stream)
              while item
              when (stringp item)
                do (format t "~A" item)
              when (eq item :done)
                do (format t "~%~%✅ Streaming beendet.~%")
                   (return))))))

(main)
