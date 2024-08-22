from kivy.lang import Builder
from kivymd.app import MDApp
from kivy.clock import Clock
from RoseVines import ChatClient
import threading

KV = '''
MDBoxLayout:
    orientation: "vertical"

    MDTopAppBar:
        title: "Chat Interface"
        
    MDBoxLayout:
        orientation: "horizontal"
        size_hint_y: None
        height: "50dp"

        MDTextField:
            id: message_input
            hint_text: "Enter your message"
            multiline: False
            on_text_validate: app.send_message(self.text)

        MDRaisedButton:
            text: "Send"
            on_release: app.send_message(message_input.text)        

    ScrollView:
        MDLabel:
            id: chat_log
            text: ""
            halign: "left"
            valign: "top"
            size_hint_y: None
            height: self.texture_size[1]
'''

class ChatApp(MDApp):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.username = 'Android'
        self.client = ChatClient(self.username, self.receive_message)
        self.client_thread = threading.Thread(target=self.client.run)
        self.client_thread.daemon = True

    def build(self):
        return Builder.load_string(KV)

    def on_start(self):
        self.client_thread.start()

    def send_message(self, message):
        if message.strip():
            print(f"Sending message: {message}")
            self.client.send_message(message)
            self.root.ids.chat_log.text += f"[You]: {message}\n"
            self.root.ids.message_input.text = ""

    def receive_message(self, username, message):
        print(f"Message received: [{username}]: {message}")
        def update_chat_log(dt):
            self.root.ids.chat_log.text += f"[{username}]: {message}\n"
        Clock.schedule_once(update_chat_log)

ChatApp().run()