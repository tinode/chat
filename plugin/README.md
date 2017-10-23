# Plugins / External Services

Tinode server supports interaction with external services referred to as plugins in this document.

Plugins are generally executed as separate processes. Tinode server communicates with them using [gRPC](https://grpc.io/) as defined in model.proto file. Plugins can be written in any of supported languages (as of the time of this writing it's C++, C#, Go, Java, Node, PHP, Python, Ruby).

Plugins have to define three methods:

 * This method is called for every message received from the client. The method returns a ServerCtrl message. If ServerCtrl.code is not 0 indicates that no further processing is needed. Server will generate a {ctrl} message from ServerCtrl and forward it to client session. If ServerCtrl.code is 0, the server should continue with default processing of the message. 	
	`rpc HandleMessage(Session, ClientMsg) returns (ServerCtrl) {}`

 * This method is called immmediately before a server message is broadcasted to topic subscribers. The filter may alter the server message or may request to drop it.
  	`rpc FilterMessage(ServerMsg) returns (ServerMsg) {}`

 * The method returns a stream of messages which are treated as if coming from a client.
  	`rpc RequestMessages(None) returns (stream ClientMsg) {}`
 
