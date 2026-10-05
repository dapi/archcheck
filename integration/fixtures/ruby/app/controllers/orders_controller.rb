class OrdersController
  def create
    PaymentGateway.charge(100)
  end
end
