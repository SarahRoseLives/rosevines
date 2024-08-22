import time
from RoseVines import ChatClient

def main():
    username = input("Enter your username: ")
    client = ChatClient(username)

    client.run()

    while True:
        message = input("Enter message (or type 'exit' to quit): ")
        if message.lower() == 'exit':
            break
        client.send_message(message)

if __name__ == "__main__":
    main()
