package hub

import (
	"context"
	"fmt"
	"net"

	"github.com/streamingfast/bstream"
	"github.com/streamingfast/logging"
	pbbstream "github.com/streamingfast/pbgo/sf/bstream/v1"
	pbheadinfo "github.com/streamingfast/pbgo/sf/headinfo/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// implementation of blockstream.Server from the hub
func (h *ForkableHub) NewBlockstreamServer(grpcServer *grpc.Server) *BlockstreamServer {

	bs := &BlockstreamServer{
		hub:        h,
		grpcServer: grpcServer,
	}

	pbheadinfo.RegisterHeadInfoServer(grpcServer, bs)
	pbbstream.RegisterBlockStreamServer(grpcServer, bs)
	return bs
}

type BlockstreamServer struct {
	hub        *ForkableHub
	grpcServer *grpc.Server
}

func (s *BlockstreamServer) Serve(lis net.Listener) error {
	<-s.hub.Ready
	return s.grpcServer.Serve(lis)
}

func (s *BlockstreamServer) Close() {
	s.grpcServer.Stop()
}

func (s *BlockstreamServer) GetHeadInfo(ctx context.Context, req *pbheadinfo.HeadInfoRequest) (*pbheadinfo.HeadInfoResponse, error) {
	num, id, t, libNum, err := s.hub.HeadInfo()
	if err != nil {
		return nil, err
	}

	resp := &pbheadinfo.HeadInfoResponse{
		LibNum:   libNum,
		HeadNum:  num,
		HeadID:   id,
		HeadTime: timestamppb.New(t),
	}
	return resp, nil
}

func (s *BlockstreamServer) Blocks(r *pbbstream.BlockRequest, stream pbbstream.BlockStream_BlocksServer) error {
	logger := logging.Logger(stream.Context(), zlog).Named("sub").Named(r.Requester)

	logger.Info("receive block request", zap.Reflect("request", r))

	h := streamHandler(stream, logger)
	var source bstream.Source

	if r.Burst == -1 {
		_, _, _, libNum, err := s.hub.HeadInfo()
		if err != nil {
			return err
		}
		source = s.hub.SourceFromBlockNumWithForks(libNum, h)
	} else if r.Burst < -1 {
		desiredBlock := uint64(-r.Burst)
		if lowestHub := s.hub.LowestBlockNum(); lowestHub > desiredBlock {
			desiredBlock = lowestHub
		}
		source = s.hub.SourceFromBlockNumWithForks(desiredBlock, h)
	} else {
		headNum, _, _, _, err := s.hub.HeadInfo()
		if err != nil {
			return err
		}
		desiredBlock := headNum - uint64(r.Burst)
		if lowestHub := s.hub.LowestBlockNum(); lowestHub > desiredBlock {
			desiredBlock = lowestHub
		}
		source = s.hub.SourceFromBlockNumWithForks(desiredBlock, h)
	}

	if source == nil {
		return fmt.Errorf("cannot get source for request %+v", r)
	}
	source.Run()
	<-source.Terminated()
	if err := source.Err(); err != nil {
		return err
	}
	return nil
}

func streamHandler(stream pbbstream.BlockStream_BlocksServer, logger *zap.Logger) bstream.Handler {
	return bstream.HandlerFunc(
		func(blk *bstream.Block, _ interface{}) error {
			block, err := blk.ToProto()
			if err != nil {
				panic(fmt.Errorf("unable to transform from bstream.Block to StreamableBlock: %w", err))
			}
			err = stream.Send(block)
			logger.Debug("block sent to stream", zap.Stringer("block", blk), zap.Error(err))
			return err
		})
}
