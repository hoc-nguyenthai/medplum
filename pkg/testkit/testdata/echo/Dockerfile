# Image thử của testkit: HTTP tĩnh với probe /health/ready, một biến môi trường in ra /env, và tắt êm khi nhận SIGTERM
# (tiến trình PID 1 phải tự bắt SIGTERM, giống một service Go thật dùng signal.NotifyContext).
FROM busybox:1.37
ARG GREETING=hello
RUN mkdir -p /www/health && echo ok > /www/health/ready && echo "$GREETING" > /www/greeting
CMD ["sh", "-c", "echo \"$WHO\" > /www/env; trap 'exit 0' TERM; httpd -f -p 8080 -h /www & wait"]
