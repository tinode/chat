"""Python implementation of a Tinode chatbot."""

from concurrent import futures
import time

import grpc

import model_pb2
import model_pb2_grpc

_ONE_DAY_IN_SECONDS = 60 * 60 * 24


class Plugin(model_pb2_grpc.PluginServicer):

	def HandleMessage(self, request, context):
		print "Request!"
		return model_pb2.ServerCtrl(code=0)
	
	def FilterMessage(self, request, context):
		return model_pb2.ServerMsg()
	
	def RequestMessages(self, request, context):
		return model_pb2.ClientMsg()


def serve():
	server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
	model_pb2_grpc.add_PluginServicer_to_server(Plugin(), server)
	server.add_insecure_port('localhost:40051')
	server.start()
	try:
		while True:
			time.sleep(_ONE_DAY_IN_SECONDS)
	except KeyboardInterrupt:
		server.stop(0)

if __name__ == '__main__':
	serve()
