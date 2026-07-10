;;;; sigoclient.lisp
;;;; Modern Common Lisp Client for sigoREST (v2.0)
;;;; Supports current models and real SSE streaming

(defpackage :sigoclient
  (:use :cl)
  (:export :*base-url*
           :ping
           :health
           :list-models
           :chat
           :chat-stream
           :get-memory
           :set-memory))

(in-package :sigoclient)

;;; Configuration

(defparameter *base-url* "http://127.0.0.1:9080"
  "Base URL of the sigoREST server")

;;; Helper functions

(defun http-get (path)
  "Perform HTTP GET request"
  (let ((url (format nil "~A~A" *base-url* path)))
    (handler-case
        (drakma:http-request url :method :get :external-format-in :utf-8)
      (error (e)
        (format *error-output* "GET error ~A: ~A~%" url e)
        nil))))

(defun http-post (path data &key (stream nil))
  "Perform HTTP POST with JSON body. If :stream t, returns the stream."
  (let* ((url (format nil "~A~A" *base-url* path))
         (json-body (with-output-to-string (s) (yason:encode data s)))
         (json-bytes (babel:string-to-octets json-body :encoding :utf-8)))
    (handler-case
        (drakma:http-request url
                             :method :post
                             :content json-bytes
                             :content-type "application/json; charset=utf-8"
                             :external-format-in :utf-8
                             :want-stream stream)
      (error (e)
        (format *error-output* "POST error ~A: ~A~%" url e)
        nil))))

(defun http-put (path data)
  "Perform HTTP PUT request"
  (let* ((url (format nil "~A~A" *base-url* path))
         (json-body (with-output-to-string (s) (yason:encode-alist data s)))
         (json-bytes (babel:string-to-octets json-body :encoding :utf-8)))
    (handler-case
        (drakma:http-request url
                             :method :put
                             :content json-bytes
                             :content-type "application/json; charset=utf-8"
                             :external-format-in :utf-8)
      (error (e)
        (format *error-output* "PUT error ~A: ~A~%" url e)
        nil))))

(defun parse-json (body)
  "Parse JSON into ALIST with keyword keys"
  (when body
    (let ((json-str (if (stringp body)
                        body
                        (babel:octets-to-string body :encoding :utf-8))))
      (handler-case
          (yason:parse json-str :object-as :alist :object-key-fn #'intern-keyword)
        (error (e)
          (format *error-output* "JSON parse error: ~A~%" e)
          nil)))))

(defun intern-keyword (name)
  "Convert JSON key to Lisp keyword"
  (intern (string-upcase (string name)) :keyword))

;;; Public API

(defun ping ()
  "Check if server is alive"
  (handler-case
      (let ((resp (http-get "/ping")))
        (and resp (string= (if (stringp resp) resp (babel:octets-to-string resp)) "pong")))
    (error () nil)))

(defun health ()
  "Get server health status"
  (parse-json (http-get "/api/health")))

(defun list-models ()
  "List all available models"
  (parse-json (http-get "/api/models")))

(defun chat (model message &key system-prompt session-id temperature max-tokens (retries 3))
  "Simple chat (backward compatible)"
  (let ((messages '()))
    (when system-prompt
      (push (list :|role| "system" :|content| system-prompt) messages))
    (push (list :|role| "user" :|content| message) messages)
    (setf messages (nreverse messages))

    (let ((payload (list (cons "model" model)
                          (cons "messages" messages)
                          (cons "retries" retries))))
      (when temperature (setf payload (append payload (list (cons "temperature" temperature)))))
      (when max-tokens (setf payload (append payload (list (cons "max_tokens" max-tokens)))))
      (when session-id (setf payload (append payload (list (cons "session_id" session-id)))))

      (let* ((resp (parse-json (http-post "/v1/chat/completions" payload)))
             (choices (cdr (assoc :choices resp)))
             (first (when choices (first choices)))
             (msg (when first (cdr (assoc :message first))))
             (content (when msg (cdr (assoc :content msg)))))
        content))))

(defun chat-stream (model message &key system-prompt session-id temperature max-tokens (retries 3))
  "Real SSE streaming. Returns a closure that yields the next token on each call."
  (let ((messages '()))
    (when system-prompt
      (push (list :|role| "system" :|content| system-prompt) messages))
    (push (list :|role| "user" :|content| message) messages)
    (setf messages (nreverse messages))

    (let ((payload (list (cons "model" model)
                          (cons "messages" messages)
                          (cons "stream" t)
                          (cons "retries" retries))))
      (when temperature (setf payload (append payload (list (cons "temperature" temperature)))))
      (when max-tokens (setf payload (append payload (list (cons "max_tokens" max-tokens)))))
      (when session-id (setf payload (append payload (list (cons "session_id" session-id)))))

      (let ((stream (http-post "/v1/chat/completions" payload :stream t)))
        (when stream
          #'(lambda ()
              (handler-case
                  (let ((line (read-line stream nil nil)))
                    (cond
                      ((null line)
                       :done)
                      (t
                       (let ((trimmed (string-trim '(#\Space #\Tab #\Return #\Newline) line)))
                         (when (plusp (length trimmed))
                           (cond
                             ((search "data: [DONE]" trimmed) :done)
                             ((search "data: " trimmed)
                              (let ((json-part (subseq trimmed 6)))
                                (handler-case
                                    (let ((data (yason:parse json-part :object-as :alist :object-key-fn #'intern-keyword)))
                                      (let ((choices (cdr (assoc :choices data))))
                                        (when (and choices (listp choices))
                                          (let ((delta (cdr (assoc :delta (first choices)))))
                                            (let ((content (cdr (assoc :content delta))))
                                              (when (and content (stringp content))
                                                content))))))
                                  (error (e)
                                    (format *error-output* "JSON parse error in stream: ~A~%" e)
                                    nil)))))
                             (t nil)))))))
                (end-of-file () :done)
                (error (c)
                  (format *error-output* "Streaming error: ~A~%" c)
                  :done))))))))))

(defun get-memory ()
  "Get global memory block"
  (parse-json (http-get "/api/memory")))

(defun set-memory (content &optional (cache t))
  "Set global memory block"
  (parse-json (http-put "/api/memory" `(("content" . ,content) ("cache" . ,cache))))))

;;; Demo

(defun demo ()
  (format t "~%=== sigoclient v2.0 (Lisp) Demo ===~%~%")
  (if (ping)
      (progn
        (format t "✅ Connected to sigoREST~%")
        (format t "Available models: ~A~%~%" (length (list-models)))
        
        (format t "Streaming test:~%")
        (let ((stream (chat-stream "cl5-s" "Schreibe ein kurzes Haiku über Common Lisp.")))
          (when (functionp stream)
            (loop for token = (funcall stream)
                  while token
                  when (stringp token)
                    do (format t "~A" token)
                  when (eq token :done)
                    do (format t "~%~%✅ Streaming finished.~%")
                       (return)))))
      (format t "❌ Server not reachable~%")))

;; Uncomment to run demo automatically
;; (demo)
