import socket
import threading
import json
import time

def get_broadcast_address():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.connect(("8.8.8.8", 80))  # Connect to a well-known address
    ip = s.getsockname()[0]
    s.close()
    ip_parts = ip.split('.')
    broadcast_address = f"{ip_parts[0]}.{ip_parts[1]}.{ip_parts[2]}.255"
    return broadcast_address

class ChatClient:
    DISCOVERY_PORT = 55000
    MESSAGE_PORT = 55001

    def __init__(self, username, message_callback=None, logging_enabled=True):
        self.username = username
        self.peer_table = {}
        self.message_callback = message_callback
        self.logging_enabled = logging_enabled

    def log(self, message):
        if self.logging_enabled:
            print(message)

    def broadcast_discovery(self):
        broadcast_address = get_broadcast_address()
        self.log(f"Broadcast address: {broadcast_address}")
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
            sock.settimeout(2)
            while True:
                message = json.dumps({'username': self.username, 'ip': self.get_ip()})
                sock.sendto(message.encode('utf-8'), (broadcast_address, self.DISCOVERY_PORT))
                self.log(f"Broadcasting discovery: {message}")
                time.sleep(5)

    def listen_for_discovery(self):
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            try:
                sock.bind(("", self.DISCOVERY_PORT))
                self.log(f"{self.username} listening for discovery on port {self.DISCOVERY_PORT}...")
                while True:
                    data, addr = sock.recvfrom(1024)
                    self.log(f"Received discovery from {addr}: {data}")
                    info = json.loads(data.decode('utf-8'))
                    peer_key = f"{info['username']}@{info['ip']}"  # Create a unique key for each peer
                    if peer_key not in self.peer_table:
                        self.peer_table[peer_key] = info['ip']
                        self.log(f"Discovered peer: {info['username']} with IP {info['ip']}")
            except OSError as e:
                self.log(f"Error in listen_for_discovery: {e}")

    def send_message(self, message):
        sender_ip = self.get_ip()
        if not self.peer_table:
            self.log("No peers available to send message.")
            return
        self.log(f"Current peer table: {self.peer_table}")
        for peer_key, peer_ip in self.peer_table.items():
            if peer_ip == sender_ip:
                continue
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                payload = json.dumps({
                    'username': self.username,
                    'ip': sender_ip,
                    'message': message
                }).encode('utf-8')
                sock.sendto(payload, (peer_ip, self.MESSAGE_PORT))
                self.log(f"Sent message to {peer_key} at {peer_ip}: {payload}")

    def listen_for_messages(self):
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            try:
                sock.bind(("", self.MESSAGE_PORT))
                self.log(f"{self.username} listening on {self.get_ip()}:{self.MESSAGE_PORT}")
                while True:
                    data, addr = sock.recvfrom(1024)
                    self.log(f"Received message data from {addr}: {data}")
                    message = json.loads(data.decode('utf-8'))
                    self.log(f"[{message['username']}]: {message['message']}")
                    if self.message_callback:
                        self.message_callback(message['username'], message['message'])
            except OSError as e:
                self.log(f"Error in listen_for_messages: {e}")

    def get_ip(self):
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            s.settimeout(0)
            s.connect(('10.254.254.254', 1))
            ip = s.getsockname()[0]
        except Exception:
            ip = '127.0.0.1'
        finally:
            s.close()
        return ip

    def run(self):
        threading.Thread(target=self.broadcast_discovery, daemon=True).start()
        threading.Thread(target=self.listen_for_discovery, daemon=True).start()
        threading.Thread(target=self.listen_for_messages, daemon=True).start()

if __name__ == "__main__":
    username = input("Enter your username: ")
    client = ChatClient(username)
    client.run()

    while True:
        time.sleep(1)
