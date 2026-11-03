"""Paper expansion reuses Chat's persisted question and direction cards."""


def contribute(context, config, bind_successor=None):
    from lazymind.chat.engine.tools.ask_user import ask_user
    return [ask_user], ['ask_user']
