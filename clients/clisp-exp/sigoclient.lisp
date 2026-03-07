;;;; sigoclient.lisp
;;;; Experimenteller Common Lisp Client für sigoREST
;;;; Minimalistisch und funktional

(defpackage :sigoclient
  (:use :cl)
  (:export :*base-url*
           :ping
           :health
           :list-models
           :chat
           :set-memory
           :get-memory))

(in-package :sigoclient)

;;; Konfiguration

(defparameter *base-url* "http://127.0.0.1:9080"
  "Basis-URL des sigoREST Servers")

;;; Hilfsfunktionen

(defun http-get (path)
  "Führt einen HTTP GET Request aus"
  (let ((url (format nil "~A~A" *base-url* path)))
    (multiple-value-bind (body status headers)
        (drakma:http-request url
                             :method :get
                             :external-format-in :utf-8)
      (declare (ignore status headers))
      body)))

(defun http-post (path data)
  "Führt einen HTTP POST Request mit JSON Body aus"
  (let* ((url (format nil "~A~A" *base-url* path))
         (json-body (with-output-to-string (s)
                      (yason:encode data s)))
         (json-bytes (babel:string-to-octets json-body :encoding :utf-8)))
    (drakma:http-request url
                         :method :post
                         :content json-bytes
                         :content-type "application/json; charset=utf-8"
                         :external-format-in :utf-8)))


(defun http-put (path data)
  "Führt einen HTTP PUT Request mit JSON Body aus"
  (let* ((url (format nil "~A~A" *base-url* path))
         (json-body (with-output-to-string (s)
                      (yason:encode-alist data s)))
         (json-bytes (babel:string-to-octets json-body :encoding :utf-8)))
    (drakma:http-request url
                         :method :put
                         :content json-bytes
                         :content-type "application/json; charset=utf-8"
                         :external-format-in :utf-8)))

(defun intern-keyword (name)
  "Konvertiert JSON Key zu Lisp Keyword"
  (intern (string-upcase name) :keyword))

(defun parse-json (body)
  "Parsed JSON Body zu Lisp Datenstrukturen"
  (when body
    (let ((json-string (if (stringp body)
                           body
                           (babel:octets-to-string body :encoding :utf-8))))
      (yason:parse json-string :object-as :alist :object-key-fn #'intern-keyword))))

;;; Öffentliche API

(defun ping ()
  "Prüft ob der Server erreichbar ist"
  (handler-case
      (let ((response (http-get "/ping")))
        (string= (if (stringp response)
                     response
                     (babel:octets-to-string response :encoding :utf-8))
                 "pong"))
    (error (e)
      (format *error-output* "Ping Fehler: ~A~%" e)
      nil)))

(defun health ()
  "Gibt den Health-Status des Servers zurück"
  (parse-json (http-get "/api/health")))

(defun list-models ()
  "Listet alle verfügbaren Modelle auf"
  (parse-json (http-get "/api/models")))

(defun chat (model message &key system-prompt session-id temperature max-tokens)
  "Sendet eine Chat-Anfrage an das Modell"
  (let ((messages (list)))
    ;; System-Prompt hinzufügen falls vorhanden
    (when system-prompt
      (let ((msg (make-hash-table :test 'equal)))
        (setf (gethash "role" msg) "system")
        (setf (gethash "content" msg) system-prompt)
        (push msg messages)))
    ;; User-Nachricht hinzufügen
    (let ((msg (make-hash-table :test 'equal)))
      (setf (gethash "role" msg) "user")
      (setf (gethash "content" msg) message)
      (push msg messages))
    ;; Reihenfolge umkehren
    (setf messages (nreverse messages))

    ;; Payload erstellen (als Hash-Tabelle für Yason)
    (let ((payload (make-hash-table :test 'equal)))
      (setf (gethash "model" payload) model)
      (setf (gethash "messages" payload) messages)
      (setf (gethash "retries" payload) 3)
      (when temperature
        (setf (gethash "temperature" payload) temperature))
      (when max-tokens
        (setf (gethash "max_tokens" payload) max-tokens))
      (when session-id
        (setf (gethash "session_id" payload) session-id))

      ;; Request senden und Antwort extrahieren
      (let* ((response (parse-json (http-post "/v1/chat/completions" payload)))
             (choices (cdr (assoc :choices response)))
             (first-choice (when (and choices (listp choices))
                            (car choices)))
             (msg (when first-choice (cdr (assoc :message first-choice))))
             (content (when msg (cdr (assoc :content msg)))))
        content))))

(defun get-memory ()
  "Gibt den aktuellen Memory-Block zurück"
  (parse-json (http-get "/api/memory")))

(defun set-memory (content &optional (cache t))
  "Setzt den globalen Memory-Block"
  (parse-json (http-put "/api/memory" `(("content" . ,content) ("cache" . ,cache)))))
