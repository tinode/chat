using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using Pbx;
using Grpc.Core;
using Grpc;
using static Pbx.Plugin;
using static Pbx.Node;
using Google.Protobuf;
using System.Threading;
using System.Collections.Concurrent;
using Newtonsoft.Json;
using Google.Protobuf.Collections;
using System.Runtime.InteropServices;
using System.IO;

namespace Tinode.ChatBot
{
    /// <summary>
    /// ChatBot Plugin implement
    /// </summary>
    public class ChatBotPlugin : PluginBase
    {
        public override Task<Unused> Account(AccountEvent request, ServerCallContext context)
        {
            string action = string.Empty;
            if (request.Action==Crud.Create)
            {
                action = "created";
            }
            else if (request.Action==Crud.Update)
            {
                action = "updated";
            }
            else if (request.Action==Crud.Delete)
            {
                action = "deleted";
            }
            else
            {
                action = "unknown";
            }
            return Task.FromResult(new Unused());
        }
    }

    /// <summary>
    /// CSharp ChatBot implement, same as the python version.
    /// </summary>
    public class ChatBot
    {
        /// <summary>
        /// Help define functionale which will be called in future.
        /// </summary>
        public class Future
        {
            /// <summary>
            /// Each rpc call message id
            /// </summary>
            public string Tid { get; private set; }
            /// <summary>
            /// Argument needs by action.
            /// </summary>
            public string Arg { get; private set; }
            /// <summary>
            /// callback function
            /// </summary>
            public Action<string, MapField<string, ByteString>> Action { get; private set; }
            /// <summary>
            /// construction
            /// </summary>
            /// <param name="tid"> Each rpc call message id</param>
            /// <param name="action">Argument needs by action.</param>
            /// <param name="arg">callback function</param>
            public Future(string tid,Action<string, MapField<string, ByteString>> action,string arg="")
            {
                Tid = tid;
                Action = action;
                Arg = arg;
            }
        }

        /// <summary>
        /// Chatbot application name
        /// </summary>
        public string AppName => "CBot";
        /// <summary>
        /// Chatbot version
        /// </summary>
        public string AppVersion => "0.15.5";
        /// <summary>
        /// Chatbot library version
        /// </summary>
        public string LibVersion => "0.15.5";
        /// <summary>
        /// Chatbot current platfrom information
        /// </summary>
        public string Platform => $"({RuntimeInformation.OSDescription} {RuntimeInformation.OSArchitecture})";
        /// <summary>
        /// Chatbot instance id, this will be used in chat
        /// </summary>
        public string BotUID { get; private set; }
        /// <summary>
        /// Next tid
        /// </summary>
        public long NextTid { get; private set; }
        /// <summary>
        /// gRPC server
        /// </summary>
        public string ServerHost { get; set; }
        /// <summary>
        /// Plugin API calls listen addr
        /// </summary>
        public string Listen { get; set; }
        /// <summary>
        /// Cookie file
        /// </summary>
        public string CookieFile { get; set; }
        /// <summary>
        /// Login in schema
        /// </summary>
        public string Schema { get; set; }
        /// <summary>
        /// Login in credentials
        /// </summary>
        public ByteString Secret { get; set; }
        /// <summary>
        /// Chatbot auto reply implement interface,you can use this to make you own chat logic
        /// </summary>
        public IBotResponse BotResponse { get; set; }

        Server server;
        AsyncDuplexStreamingCall<ClientMsg, ServerMsg> client;
        Channel channel;
        CancellationTokenSource cancellationTokenSource;
        Queue<ClientMsg> sendMsgQueue;
        Dictionary<string, bool> subscriptions;
        Dictionary<string, Future> onCompletion;

        /// <summary>
        /// Contruction
        /// </summary>
        /// <param name="serverHost">gRPC server</param>
        /// <param name="listen">Plugin API calls listen addr</param>
        /// <param name="cookie">Cookie file</param>
        /// <param name="schema">Login in schema</param>
        /// <param name="secret">Login in credentials</param>
        /// <param name="botResponse">Chatbot auto reply implement interface,you can use this to make you own chat logic</param>
        public ChatBot(string serverHost="localhost:6061",string listen="0.0.0.0:40052",string cookie=".tn-cookie",string schema="basic",string secret="",IBotResponse botResponse=null)
        {
            //Initial a tid with a random value btw 1~1000
            NextTid =new Random().Next(1,1000);
            ServerHost = serverHost;
            Listen = listen;
            CookieFile = cookie;
            Schema = schema;
            Secret = ByteString.CopyFromUtf8(secret);
            BotResponse = botResponse;
            cancellationTokenSource = new CancellationTokenSource();
            sendMsgQueue = new Queue<ClientMsg>();
            subscriptions = new Dictionary<string, bool>();
            onCompletion = new Dictionary<string, Future>();
        }

        /// <summary>
        /// generate the next tid
        /// </summary>
        /// <returns>new tid</returns>
        public string GetNextTid()
        {
            NextTid += 1;
            return NextTid.ToString();
        }
        
        /// <summary>
        /// Initialize plugin api calls listen server
        /// </summary>
        /// <returns>Plugin api calls server</returns>
        public Server InitServer()
        {
            var server = new Server();
            server.Services.Add(Plugin.BindService(new ChatBotPlugin()));
            var listenHost = Listen.Split(':')[0];
            var listenPort = int.Parse(Listen.Split(':')[1]);
            server.Ports.Add(new ServerPort(listenHost,listenPort, ServerCredentials.Insecure));
            server.Start();
            return server;
        }

        /// <summary>
        /// Initialize chatbot client
        /// </summary>
        /// <returns>chatbot client instance</returns>
        public AsyncDuplexStreamingCall<ClientMsg, ServerMsg> InitClient()
        {
            channel = new Channel(ServerHost, ChannelCredentials.Insecure);
            var stub = new NodeClient(channel);
            var stream = stub.MessageLoop(cancellationToken:cancellationTokenSource.Token);
            ClientPost(Hello());
            ClientPost(Login(CookieFile, Schema, Secret));
            ClientPost(Subscribe("me"));
            return stream;
        }

        /// <summary>
        /// Add future callback
        /// </summary>
        /// <param name="tid">tid</param>
        /// <param name="bundle">callback instance</param>
        public void AddFuture(string tid,Future bundle)
        {
            onCompletion.Add(tid, bundle);
        }

        /// <summary>
        /// Execute callbacks in future callback collection
        /// </summary>
        /// <param name="tid">tid</param>
        /// <param name="code">rpc status code</param>
        /// <param name="text">text</param>
        /// <param name="paramaters">paramaters</param>
        public void ExecFuture(string tid,int code,string text,MapField<string,ByteString> paramaters)
        {
            if (onCompletion.ContainsKey(tid))
            {
                var bundle = onCompletion[tid];
                onCompletion.Remove(tid);
                if (code>=200 && code<400)
                {
                    var arg = bundle.Arg;
                    bundle.Action(arg, paramaters);
                }
                else
                {
                    Console.WriteLine($"Error:{code}, {text}");
                }
            }

        }

        /// <summary>
        /// add a chat topic to subscription
        /// </summary>
        /// <param name="topic">topic name </param>
        public void AddSubscription(string topic)
        {
            if (!subscriptions.ContainsKey(topic))
            {
                subscriptions.Add(topic, true);
            }
        }

        /// <summary>
        /// delete a chat topic from subscription
        /// </summary>
        /// <param name="topic">topic name </param>
        public void DelSubscription(string topic)
        {
            if (subscriptions.ContainsKey(topic))
            {
                subscriptions.Remove(topic);
            }
        }

        /// <summary>
        /// Server version callback implement
        /// </summary>
        /// <param name="paramaters">paramaters</param>
        public void ServerVersion(MapField<string, ByteString> paramaters)
        {
            if (paramaters==null)
            {
                return;
            }
            Console.WriteLine($"Server:{paramaters["build"].ToString(Encoding.ASCII)},{paramaters["ver"].ToString(Encoding.ASCII)}");
        }

        /// <summary>
        /// login callback implement
        /// </summary>
        /// <param name="cookieFile">cookie file</param>
        /// <param name="paramaters">paramaters</param>
        public void OnLogin(string cookieFile, MapField<string, ByteString> paramaters)
        {
            if (paramaters == null || string.IsNullOrEmpty(cookieFile))
            {
                return;
            }
            if (paramaters.ContainsKey("user"))
            {
                BotUID = paramaters["user"].ToString(Encoding.ASCII);
            }
            Dictionary<string, string> cookieDics = new Dictionary<string, string>();
            cookieDics["schema"] = "token";
            if (paramaters.ContainsKey("token"))
            {
                cookieDics["secret"] = JsonConvert.DeserializeObject<string>(paramaters["token"].ToString(Encoding.UTF8));
                cookieDics["expires"] = JsonConvert.DeserializeObject<string>(paramaters["expires"].ToString(Encoding.UTF8));
            }
            else
            {
                cookieDics["schema"] = "basic";
                cookieDics["secret"] = JsonConvert.DeserializeObject<string>(paramaters["token"].ToString(Encoding.UTF8));
            }
            try
            {
                using (FileStream stream = new FileStream(cookieFile, FileMode.Create,FileAccess.Write))
                using (StreamWriter w = new StreamWriter(stream))
                {
                    w.Write(JsonConvert.SerializeObject(cookieDics));
                }

            }
            catch (Exception e)
            {
                Console.WriteLine($"Failed to save authentication cookie:{e}");
            }

        }


        /// <summary>
        /// read auth information from cookie
        /// </summary>
        /// <param name="schema">schema type</param>
        /// <param name="secret">secret</param>
        /// <returns>success-true, faild-false</returns>
        public bool ReadAuthCookie(out string schema,out ByteString secret)
        {
            schema = null;
            secret =null;
            if (!File.Exists(CookieFile))
            {
                return false;
            }

            try
            {
                using (FileStream stream = new FileStream(CookieFile, FileMode.Open,FileAccess.Read))
                using (StreamReader r = new StreamReader(stream))
                {
                    var cookies = JsonConvert.DeserializeObject<Dictionary<string, string>>(r.ReadToEnd());
                    schema = cookies["schema"];
                    secret = null;
                    if (schema == "token")
                    {
                        var defautl = Encoding.Default.GetBytes(cookies["secret"]);
                        var utf8Str = Encoding.UTF8.GetString(defautl);
                        secret = ByteString.FromBase64(utf8Str);
                    }
                    else
                    {
                        secret = ByteString.CopyFromUtf8(cookies["secret"]);
                    }
                    return true;

                }
            }
            catch (Exception)
            {
                return false;
            }
            
        }

        /// <summary>
        /// Post message to message queue
        /// </summary>
        /// <param name="msg">message</param>
        public void ClientPost(ClientMsg msg)
        {
            sendMsgQueue.Enqueue(msg);
        }

        /// <summary>
        /// reset client status
        /// </summary>
        public void ClientReset()
        {
            try
            {
                subscriptions.Clear();
                onCompletion.Clear();
                while (sendMsgQueue.Count > 0)
                {
                    sendMsgQueue.Dequeue();
                }
            }
            catch (Exception e)
            {
            }
        }

        /// <summary>
        /// Say Hi to server
        /// </summary>
        /// <returns>Hi message</returns>
        public ClientMsg Hello()
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((unused, paramaters) =>
            {
                ServerVersion(paramaters);
            })));

            return new ClientMsg() { Hi = new ClientHi() { Id = tid, UserAgent = $"{AppName}/{AppVersion} {Platform}; gRPC-csharp/{AppVersion}", Ver = LibVersion, Lang = "EN" } };
        }

        /// <summary>
        /// login in
        /// </summary>
        /// <param name="cookieFile">cookie file</param>
        /// <param name="scheme">schema type</param>
        /// <param name="secret">secret</param>
        /// <returns>login in message</returns>
        public ClientMsg Login(string cookieFile,string scheme,ByteString secret)
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((fname, paramaters) =>
            {
                OnLogin(fname, paramaters);
            }),cookieFile));

            return new ClientMsg() { Login = new ClientLogin() { Id = tid, Scheme = scheme, Secret = secret } };
        }

        /// <summary>
        /// Subscribe topic
        /// </summary>
        /// <param name="topic">topic name</param>
        /// <returns>subscribe message</returns>
        public ClientMsg Subscribe(string topic)
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((topicName, unused) =>
            {
                AddSubscription(topicName);
            }),topic));

            return new ClientMsg() { Sub = new ClientSub() { Id = tid, Topic = topic } };
        }

        /// <summary>
        /// leave topic
        /// </summary>
        /// <param name="topic">topic name</param>
        /// <returns>leave message</returns>
        public ClientMsg Leave(string topic)
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((topicName, unused) =>
            {
                DelSubscription(topicName);
            }), topic));

            return new ClientMsg() { Leave = new ClientLeave() { Id = tid, Topic = topic } };
        }

        /// <summary>
        /// publish message to a topic 
        /// </summary>
        /// <param name="topic">topic name</param>
        /// <param name="text">message</param>
        /// <returns>publish message</returns>
        public ClientMsg Publish(string topic,string text)
        {
            var tid = GetNextTid();
            var message = ByteString.CopyFromUtf8(JsonConvert.SerializeObject(text));

            return new ClientMsg() { Pub = new ClientPub() { Id = tid, Topic = topic, NoEcho = true, Content = message } };
        }

        /// <summary>
        /// note read
        /// </summary>
        /// <param name="topic">topic name </param>
        /// <param name="seq">chat sequence id</param>
        /// <returns>note message</returns>
        public ClientMsg NoteRead(string topic, int seq)
        {
            return new ClientMsg() { Note = new ClientNote() { SeqId = seq, Topic = topic, What = InfoNote.Read } };
        }

        /// <summary>
        /// sending message queue loop
        /// </summary>
        public void SendMessageLoop()
        {
            Task sendBackendTask = new Task(async () =>
            {
                Console.WriteLine("Message send queue started...");
                while (!cancellationTokenSource.IsCancellationRequested)
                {
                    if (sendMsgQueue.Count > 0)
                    {
                        var msg = sendMsgQueue.Dequeue();
                        try
                        {
                            await client.RequestStream.WriteAsync(msg);
                        }
                        catch (Exception e)
                        {
                            Console.WriteLine($"[Error] {e.Message} ,Failed message will be put back to queue...");
                            sendMsgQueue.Enqueue(msg);
                            Thread.Sleep(1000);
                        }


                    }
                    else
                    {
                        Thread.Sleep(10);
                    }
                }
                Console.WriteLine("Detect cancel message,stop sending message...");
            }, cancellationTokenSource.Token);
            sendBackendTask.Start();

        }

        /// <summary>
        /// client receive message loop
        /// </summary>
        /// <returns></returns>
        public async Task ClientMessageLoop()
        {
            while (!cancellationTokenSource.IsCancellationRequested)
            {
                if (!await client.ResponseStream.MoveNext())
                {
                    break;
                }
                var response = client.ResponseStream.Current;
                if (response.Ctrl != null)
                {
                    Console.WriteLine($"Ctrl:{response.Ctrl.Id},{response.Ctrl.Code},{response.Ctrl.Text},{response.Ctrl.Params}");
                    ExecFuture(response.Ctrl.Id, response.Ctrl.Code, response.Ctrl.Text, response.Ctrl.Params);
                }
                else if (response.Data != null)
                {
                    if (response.Data.FromUserId != BotUID)
                    {
                        ClientPost(NoteRead(response.Data.Topic, response.Data.SeqId));
                        Thread.Sleep(50);
                        if (BotResponse != null)
                        {
                            ClientPost(Publish(response.Data.Topic, BotResponse.ThinkAndReply(response.Data.Clone())));
                        }
                        else
                        {
                            ClientPost(Publish(response.Data.Topic, "I don't know how to talk with you, maybe my father didn't put my brain in my head..."));
                        }

                    }
                }
                else if (response.Pres != null)
                {
                    if (response.Pres.Topic == "me")
                    {
                        if ((response.Pres.What == ServerPres.Types.What.On || response.Pres.What == ServerPres.Types.What.Msg) && !subscriptions.ContainsKey(response.Pres.Src))
                        {
                            ClientPost(Subscribe(response.Pres.Src));
                        }
                        else if (response.Pres.What == ServerPres.Types.What.Off && subscriptions.ContainsKey(response.Pres.Src))
                        {
                            ClientPost(Leave(response.Pres.Src));
                        }
                    }
                }
            }
        }

        /// <summary>
        /// start chatbot
        /// </summary>
        /// <returns></returns>
        public async Task Start()
        {

            server= InitServer();
            client = InitClient();
            SendMessageLoop();
            while (!cancellationTokenSource.IsCancellationRequested)
            {
                try
                {
                    await ClientMessageLoop();
                }
                catch (Exception e)
                {
                    Console.WriteLine($"Connection Closed:{e.Message}");
                    Thread.Sleep(3000);
                    ClientReset();
                    client = InitClient();
                }
            }

        } 
        
        /// <summary>
        /// stop chatbot
        /// </summary>
        public void Stop()
        {
            Console.WriteLine("[Exit] ChatBot is exiting...Wait a second...");
            cancellationTokenSource.Cancel();
            server.ShutdownAsync().Wait();
            channel.ShutdownAsync().Wait();

        }

    }
}