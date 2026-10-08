FROM python:3.12-alpine
COPY app.py /app/app.py
RUN chmod 755 /app/app.py
EXPOSE 8000
ENTRYPOINT ["python", "/app/app.py"]
