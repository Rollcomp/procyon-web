package web

import (
	"github.com/google/uuid"
	context "github.com/procyon-projects/procyon-context"
	core "github.com/procyon-projects/procyon-core"
	tx "github.com/procyon-projects/procyon-tx"
)

type TransactionContext struct {
	context.ConfigurableApplicationContext
	transactionalContext tx.TransactionalContext
	logger               core.Logger
}

func prepareTransactionContext(context context.ConfigurableApplicationContext) *TransactionContext {
	transactionalContext := tx.NewSimpleTransactionalContext(nil, nil)
	txContext := &TransactionContext{
		context,
		transactionalContext,
		core.NewSimpleLogger(),
	}
	// configure logger
	txContext.configureLogger()
	return txContext
}

func (ctx TransactionContext) GetContextId() uuid.UUID {
	return ctx.transactionalContext.GetContextId()
}

func (ctx TransactionContext) GetLogger() core.Logger {
	return ctx.logger
}

func (ctx TransactionContext) configureLogger() {
	extensibleLoggerFormatter := core.NewSimpleExtensibleLogFormatter(ctx.GetAppId().String(), ctx.GetContextId().String())
	ctx.logger.(*core.SimpleLogger).SetExtensibleLogFormatter(extensibleLoggerFormatter)
}