import os
import time
import psycopg2
from flask import Flask, jsonify, request

app = Flask(__name__)

def get_db():
    return psycopg2.connect(os.environ["DATABASE_URL"])

def wait_for_db(max_retries=30, delay=1):
    for i in range(max_retries):
        try:
            conn = psycopg2.connect(os.environ["DATABASE_URL"])
            conn.close()
            print(f"Database ready (attempt {i + 1})")
            return
        except psycopg2.OperationalError:
            print(f"Waiting for database... (attempt {i + 1}/{max_retries})")
            time.sleep(delay)
    raise RuntimeError("Could not connect to database")

def init_db():
    wait_for_db()
    conn = get_db()
    cur = conn.cursor()
    cur.execute("""
        CREATE TABLE IF NOT EXISTS notes (
            id SERIAL PRIMARY KEY,
            content TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT NOW()
        )
    """)
    conn.commit()
    cur.close()
    conn.close()

@app.route("/")
def health():
    return jsonify({"status": "ok", "message": "App connected to Postgres"})

@app.route("/notes", methods=["GET"])
def list_notes():
    conn = get_db()
    cur = conn.cursor()
    cur.execute("SELECT id, content, created_at FROM notes ORDER BY created_at DESC")
    notes = [{"id": r[0], "content": r[1], "created_at": str(r[2])} for r in cur.fetchall()]
    cur.close()
    conn.close()
    return jsonify(notes)

@app.route("/notes", methods=["POST"])
def create_note():
    data = request.get_json()
    conn = get_db()
    cur = conn.cursor()
    cur.execute("INSERT INTO notes (content) VALUES (%s) RETURNING id, content, created_at", (data["content"],))
    row = cur.fetchone()
    conn.commit()
    cur.close()
    conn.close()
    return jsonify({"id": row[0], "content": row[1], "created_at": str(row[2])}), 201

if __name__ == "__main__":
    init_db()
    app.run(host="0.0.0.0", port=3000)
