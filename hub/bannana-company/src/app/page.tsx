import Image from "next/image";

export default function Home() {
  return (
    <div className="min-h-screen bg-gradient-to-b from-yellow-50 to-white">
      {/* Hero Section */}
      <section className="relative h-screen flex items-center justify-center px-4 sm:px-6 lg:px-8">
        <div className="max-w-7xl mx-auto text-center">
          <div className="mb-8">
            <span className="inline-flex items-center px-4 py-1 rounded-full text-sm font-medium bg-yellow-100 text-yellow-800">
              Backed by Y Combinator
            </span>
          </div>
          <h1 className="text-4xl sm:text-6xl font-bold text-gray-900 mb-6">
            The Future of Bananas is Here
          </h1>
          <p className="text-xl text-gray-600 mb-8 max-w-2xl mx-auto">
            We're revolutionizing the banana industry with sustainable farming practices and direct-to-consumer delivery.
          </p>
          <div className="flex gap-4 justify-center">
            <button className="bg-yellow-500 text-white px-8 py-3 rounded-full font-medium hover:bg-yellow-600 transition-colors">
              Order Now
            </button>
            <button className="border-2 border-yellow-500 text-yellow-500 px-8 py-3 rounded-full font-medium hover:bg-yellow-50 transition-colors">
              Learn More
            </button>
          </div>
        </div>
      </section>

      {/* Features Section */}
      <section className="py-20 px-4 sm:px-6 lg:px-8 bg-white">
        <div className="max-w-7xl mx-auto">
          <h2 className="text-3xl font-bold text-center mb-12">Why Choose Our Bananas?</h2>
          <div className="grid grid-cols-1 md:grid-cols-3 gap-8">
            <div className="p-6 rounded-xl bg-yellow-50">
              <div className="w-12 h-12 bg-yellow-500 rounded-full flex items-center justify-center mb-4">
                <svg className="w-6 h-6 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                </svg>
              </div>
              <h3 className="text-xl font-semibold mb-2">Sustainable Farming</h3>
              <p className="text-gray-600">Our bananas are grown using eco-friendly practices that protect the environment.</p>
            </div>
            <div className="p-6 rounded-xl bg-yellow-50">
              <div className="w-12 h-12 bg-yellow-500 rounded-full flex items-center justify-center mb-4">
                <svg className="w-6 h-6 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
              <h3 className="text-xl font-semibold mb-2">Next-Day Delivery</h3>
              <p className="text-gray-600">Fresh bananas delivered to your doorstep within 24 hours of harvest.</p>
            </div>
            <div className="p-6 rounded-xl bg-yellow-50">
              <div className="w-12 h-12 bg-yellow-500 rounded-full flex items-center justify-center mb-4">
                <svg className="w-6 h-6 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
                </svg>
              </div>
              <h3 className="text-xl font-semibold mb-2">Quality Guaranteed</h3>
              <p className="text-gray-600">Every banana is hand-selected to ensure perfect ripeness and taste.</p>
            </div>
          </div>
        </div>
      </section>

      {/* CTA Section */}
      <section className="py-20 px-4 sm:px-6 lg:px-8 bg-yellow-500">
        <div className="max-w-7xl mx-auto text-center">
          <h2 className="text-3xl font-bold text-white mb-8">Ready to Experience the Best Bananas?</h2>
          <button className="bg-white text-yellow-500 px-8 py-3 rounded-full font-medium hover:bg-yellow-50 transition-colors">
            Start Your Subscription
          </button>
        </div>
      </section>

      {/* Footer */}
      <footer className="py-8 px-4 sm:px-6 lg:px-8 bg-gray-900 text-white">
        <div className="max-w-7xl mx-auto flex flex-col md:flex-row justify-between items-center">
          <div className="mb-4 md:mb-0">
            <p className="text-sm">© 2024 Banana Company. All rights reserved.</p>
          </div>
          <div className="flex gap-6">
            <a href="#" className="text-sm hover:text-yellow-500 transition-colors">About</a>
            <a href="#" className="text-sm hover:text-yellow-500 transition-colors">Contact</a>
            <a href="#" className="text-sm hover:text-yellow-500 transition-colors">Privacy</a>
          </div>
        </div>
      </footer>
    </div>
  );
}
